package analyze

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults for Options fields left at zero.
const (
	DefaultMaxFiles      = 20000
	DefaultMaxFileBytes  = 1 << 20
	DefaultGitMaxCommits = 2000
)

// fileInfo is the working state for one file during a scan. The exported
// File is built from it at the end.
type fileInfo struct {
	idx       int
	rel       string // slash path relative to the root
	dir       string // slash dir ("" = root)
	base      string
	abs       string
	size      int64
	lang      string
	content   []byte // nil when not read (too big, binary, limit)
	read      bool   // readFile ran
	tooBig    bool
	spec      bool // looks like an OpenAPI / Swagger document
	binary    bool
	lines     int
	code      int
	todos     int
	test      bool
	generated bool
	minified  bool
	parsed    bool
	parseErr  bool
	pkg       string

	syms      []Symbol
	imports   []rawImport
	bindings  map[string]binding
	externals []extRef
	endpoints []epRaw
	mounts    []mountRaw
	envs      []envUse
	entries   []EntryPoint
	goF       *goFile
	refText   string   // comment/string-free text (only while parsing)
	refIdents []string // sorted identifiers of refText, for the refs heuristic
}

// rawImport is one import statement as written.
type rawImport struct {
	spec   string
	names  []string
	line   int
	level  int    // Python relative-import dots
	target string // resolved file inside the folder ("" = external / unknown)
}

// binding maps a local name to the import it came from (for route mounts).
type binding struct {
	imp  int    // index into fileInfo.imports
	name string // imported member, "" = the module itself / default export
}

// extRef is a package imported by a file that lives outside the folder.
type extRef struct{ eco, name string }

// epRaw is an endpoint before mount prefixes are applied.
type epRaw struct {
	Endpoint
	node string // mount graph node it belongs to ("" = never prefixed)
}

// mountRaw says "routes of child are served under parent + prefix".
type mountRaw struct {
	parent   string
	child    string // resolved node key when known at parse time
	prefix   string
	line     int
	explicit bool   // an explicit Mount() (unresolved → one catch-all endpoint)
	handler  string // expression text for the catch-all
	fw       string
	// Deferred resolution (done while linking):
	bind   string // JS/Python local binding name
	attr   string // Python attribute (users.router → router)
	module string // Django include("app.urls")
	goQual string // Go package alias
	goFunc string // Go function name
}

// envUse is one read of an environment variable.
type envUse struct {
	name string
	def  string
	line int
}

// analyzer holds one scan.
type analyzer struct {
	ctx      context.Context
	root     string
	opt      Options
	progress Progress
	files    []*fileInfo
	byPath   map[string]*fileInfo
	skipped  int
	warnings []string
	res      *Result

	skippedDirs map[string]int
	limitHit    bool

	goMods   []goModule
	ts       *tsResolver
	py       *pyIndex
	javaPkgs map[string][]int // Java/Kotlin package → file indices

	extDeclared map[string]*External // eco+"\x00"+norm name
	tables      map[string]*Table
	tableOrder  []string
	services    []Service
	specFiles   []SpecFile
	entries     []EntryPoint
	envDocs     map[string]bool
	envDocOnly  map[string]bool
	mdFiles     []*fileInfo
	composeEnv  map[string]bool
	goPkgNames  map[string]string // dir → Go package name
	localCrates []string
}

// Run scans root and returns what it found. progress may be nil.
func Run(ctx context.Context, root string, opt Options, progress Progress) (*Result, error) {
	if opt.MaxFiles <= 0 {
		opt.MaxFiles = DefaultMaxFiles
	}
	if opt.MaxFileBytes <= 0 {
		opt.MaxFileBytes = DefaultMaxFileBytes
	}
	if opt.GitMaxCommits == 0 {
		opt.GitMaxCommits = DefaultGitMaxCommits
	}
	if progress == nil {
		progress = func(string, int, int) {}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New("not a folder: " + root)
	}
	a := &analyzer{
		ctx: ctx, root: real, opt: opt, progress: progress,
		byPath:      map[string]*fileInfo{},
		skippedDirs: map[string]int{},
		extDeclared: map[string]*External{},
		tables:      map[string]*Table{},
		envDocs:     map[string]bool{},
		envDocOnly:  map[string]bool{},
		composeEnv:  map[string]bool{},
		goPkgNames:  map[string]string{},
		javaPkgs:    map[string][]int{},
	}
	a.res = &Result{Name: filepath.Base(abs)}

	if err := a.walk(); err != nil {
		return nil, err
	}
	sortFiles(a.files)
	for _, f := range a.files {
		a.byPath[f.rel] = f
	}

	// Read, measure and parse in one pass so only the files in flight (and
	// what later steps need) are held in memory.
	a.prepare()
	if err := a.parallel("parsing", func(f *fileInfo) {
		if !f.read {
			a.readFile(f)
		}
		a.parseFile(f)
		detach(f)
		if !a.keepContent(f) {
			f.content = nil
		}
	}); err != nil {
		return nil, err
	}
	// Drop binaries so indices stay dense.
	kept := a.files[:0]
	for _, f := range a.files {
		if f.binary {
			a.skipped++
			delete(a.byPath, f.rel)
			continue
		}
		kept = append(kept, f)
	}
	a.files = kept
	for i, f := range a.files {
		f.idx = i
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	progress("linking", 0, 1)
	a.special()
	a.link()
	progress("linking", 1, 1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if opt.GitMaxCommits != -1 {
		progress("history", 0, 1)
		a.git()
		progress("history", 1, 1)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	a.finish()
	return a.res, nil
}

// parallel runs fn over every file with one worker per CPU, reporting
// progress from the calling goroutine at most ten times a second.
func (a *analyzer) parallel(stage string, fn func(*fileInfo)) error {
	total := len(a.files)
	var next, done int64
	workers := runtime.GOMAXPROCS(0)
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if a.ctx.Err() != nil {
					return
				}
				i := int(atomic.AddInt64(&next, 1)) - 1
				if i >= total {
					return
				}
				func() {
					defer func() {
						// A parser bug on one odd file must not kill the scan.
						if r := recover(); r != nil {
							a.files[i].parseErr = true
							a.files[i].parsed = false
						}
					}()
					fn(a.files[i])
				}()
				atomic.AddInt64(&done, 1)
			}
		}()
	}
	fin := make(chan struct{})
	go func() { wg.Wait(); close(fin) }()
	a.progress(stage, 0, total)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-fin:
			a.progress(stage, total, total)
			return a.ctx.Err()
		case <-tick.C:
			a.progress(stage, int(atomic.LoadInt64(&done)), total)
		}
	}
}

// parseFile dispatches one file to its language parser.
func (a *analyzer) parseFile(f *fileInfo) {
	if f.content == nil || f.minified {
		return
	}
	f.spec = looksLikeSpec(f)
	switch f.lang {
	case "Go":
		a.parseGo(f)
	case "TypeScript", "JavaScript", "Vue", "Svelte":
		a.parseJS(f)
	case "Python":
		a.parsePython(f)
	case "Rust":
		parseRust(f)
	case "Java", "Kotlin":
		parseJava(f)
	case "C#":
		parseCSharp(f)
	case "PHP":
		parsePHP(f)
	case "Ruby":
		parseRuby(f)
	}
}

// detach copies every string a parser kept out of the file text, so the
// (large) text can be freed: substrings would pin it in memory.
func detach(f *fileInfo) {
	c := strings.Clone
	for i := range f.syms {
		s := &f.syms[i]
		s.Name, s.Parent, s.Signature, s.Doc = c(s.Name), c(s.Parent), c(s.Signature), c(s.Doc)
	}
	hasTarget := false
	for i := range f.imports {
		im := &f.imports[i]
		im.spec = c(im.spec)
		for k := range im.names {
			im.names[k] = c(im.names[k])
		}
		if im.target != "" {
			hasTarget = true
		}
	}
	if len(f.bindings) > 0 {
		nb := make(map[string]binding, len(f.bindings))
		for k, v := range f.bindings {
			v.name = c(v.name)
			nb[c(k)] = v
		}
		f.bindings = nb
	}
	for i := range f.externals {
		f.externals[i].name = c(f.externals[i].name)
	}
	for i := range f.endpoints {
		e := &f.endpoints[i].Endpoint
		e.Method, e.Path, e.Handler, e.Summary, e.Tag = c(e.Method), c(e.Path), c(e.Handler), c(e.Summary), c(e.Tag)
	}
	for i := range f.mounts {
		m := &f.mounts[i]
		m.prefix, m.handler, m.bind, m.attr, m.module, m.goQual, m.goFunc = c(m.prefix), c(m.handler), c(m.bind), c(m.attr), c(m.module), c(m.goQual), c(m.goFunc)
	}
	for i := range f.envs {
		f.envs[i].name, f.envs[i].def = c(f.envs[i].name), c(f.envs[i].def)
	}
	for i := range f.entries {
		f.entries[i].Detail = c(f.entries[i].Detail)
	}
	f.pkg = c(f.pkg)
	if f.refText != "" {
		if hasTarget {
			set := identSet(f.refText)
			f.refIdents = make([]string, 0, len(set))
			for k := range set {
				f.refIdents = append(f.refIdents, c(k))
			}
			sort.Strings(f.refIdents)
		}
		f.refText = ""
	}
}

// warnf adds a plain-language warning.
func (a *analyzer) warnf(format string, args ...any) {
	a.warnings = append(a.warnings, fmt.Sprintf(format, args...))
}

// finish builds the exported Result, sorted and with non-nil slices.
func (a *analyzer) finish() {
	r := a.res
	if r.Files == nil {
		r.Files = []File{}
	}
	nonNil(r)
	r.Warnings = append(r.Warnings, a.warnings...)
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	s := &r.Stats
	s.Files = len(r.Files)
	s.Dirs = len(r.Dirs)
	s.Symbols = len(r.Symbols)
	s.Edges = len(r.Edges)
	s.Endpoints = len(r.Endpoints)
	s.Tables = len(r.Tables)
	s.EnvVars = len(r.EnvVars)
	s.Externals = len(r.Externals)
	s.Cycles = len(r.Cycles)
	s.Skipped = a.skipped
	for _, f := range r.Files {
		if f.Lang != "" {
			s.CodeFiles++
		}
		s.Lines += f.Lines
		s.Code += f.Code
		s.Bytes += f.Bytes
		s.Todos += f.Todos
		if f.Test {
			s.Tests++
		}
	}
}

func nonNil(r *Result) {
	if r.Dirs == nil {
		r.Dirs = []Dir{}
	}
	if r.Symbols == nil {
		r.Symbols = []Symbol{}
	}
	if r.Edges == nil {
		r.Edges = []Edge{}
	}
	if r.Calls == nil {
		r.Calls = []Call{}
	}
	if r.Externals == nil {
		r.Externals = []External{}
	}
	if r.Endpoints == nil {
		r.Endpoints = []Endpoint{}
	}
	if r.SpecFiles == nil {
		r.SpecFiles = []SpecFile{}
	}
	if r.Tables == nil {
		r.Tables = []Table{}
	}
	for i := range r.Tables {
		if r.Tables[i].Columns == nil {
			r.Tables[i].Columns = []Column{}
		}
	}
	if r.EnvVars == nil {
		r.EnvVars = []EnvVar{}
	}
	for i := range r.EnvVars {
		if r.EnvVars[i].Uses == nil {
			r.EnvVars[i].Uses = []string{}
		}
	}
	if r.Services == nil {
		r.Services = []Service{}
	}
	if r.EntryPoints == nil {
		r.EntryPoints = []EntryPoint{}
	}
	if r.Languages == nil {
		r.Languages = []LangStat{}
	}
	if r.Cycles == nil {
		r.Cycles = [][]string{}
	}
	if r.Git != nil && r.Git.Authors == nil {
		r.Git.Authors = []Author{}
	}
}

// --- small shared helpers ---

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniqSorted(in []string) []string {
	if len(in) == 0 {
		return in
	}
	sort.Strings(in)
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
