package analyze

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// prepare builds the indexes parsers need (go.mod modules, tsconfig
// aliases, Python source roots).
func (a *analyzer) prepare() {
	for _, f := range a.files {
		if f.base == "go.mod" || f.base == "package.json" ||
			(strings.HasPrefix(f.base, "tsconfig") || strings.HasPrefix(f.base, "jsconfig")) && strings.HasSuffix(f.base, ".json") {
			a.readFile(f)
		}
	}
	a.prepareGo()
	a.prepareJS()
	a.preparePython()
}

// special reads the non-code inputs: manifests, config, specs, schemas.
func (a *analyzer) special() {
	a.manifests()
	a.config()
	a.openapi()
	a.sqlFiles()
}

type edgeAcc struct {
	names map[string]bool
}

type symRef struct {
	f *fileInfo
	i int
}

// link connects everything: edges, calls, refs, externals, endpoints with
// mount prefixes, env vars, folders, cycles and the headline numbers.
func (a *analyzer) link() {
	r := a.res
	edges := map[[2]int]*edgeAcc{}
	addEdge := func(from, to *fileInfo, names []string) {
		if from == to {
			return
		}
		k := [2]int{from.idx, to.idx}
		e := edges[k]
		if e == nil {
			e = &edgeAcc{names: map[string]bool{}}
			edges[k] = e
		}
		for _, n := range names {
			e.names[n] = true
		}
	}

	calls := a.linkGo(addEdge)
	a.linkJava()
	for _, f := range a.files {
		for _, im := range f.imports {
			if im.target == "" {
				continue
			}
			if t := a.byPath[im.target]; t != nil {
				addEdge(f, t, im.names)
			}
		}
	}

	// Edges, sorted, plus per-file counts.
	keys := make([][2]int, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		fi, fj := a.files[keys[i][0]].rel, a.files[keys[j][0]].rel
		if fi != fj {
			return fi < fj
		}
		return a.files[keys[i][1]].rel < a.files[keys[j][1]].rel
	})
	imports := make([]int, len(a.files))
	importedBy := make([]int, len(a.files))
	importers := make([][]int, len(a.files)) // file → files importing it (edge keys)
	r.Edges = make([]Edge, 0, len(keys))
	for _, k := range keys {
		e := edges[k]
		names := sortedKeys(e.names)
		if len(names) == 0 {
			names = nil
		}
		r.Edges = append(r.Edges, Edge{From: a.files[k[0]].rel, To: a.files[k[1]].rel, Names: names})
		imports[k[0]]++
		importedBy[k[1]]++
		importers[k[1]] = append(importers[k[1]], len(r.Edges)-1)
	}

	// Calls.
	r.Calls = make([]Call, 0, len(calls))
	for k, n := range calls {
		r.Calls = append(r.Calls, Call{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(r.Calls, func(i, j int) bool {
		if r.Calls[i].From != r.Calls[j].From {
			return r.Calls[i].From < r.Calls[j].From
		}
		return r.Calls[i].To < r.Calls[j].To
	})

	a.nonGoRefs(importers)

	// Symbols.
	for _, f := range a.files {
		r.Symbols = append(r.Symbols, f.syms...)
	}
	sort.SliceStable(r.Symbols, func(i, j int) bool {
		x, y := r.Symbols[i], r.Symbols[j]
		if x.File != y.File {
			return x.File < y.File
		}
		if x.Line != y.Line {
			return x.Line < y.Line
		}
		return x.Name < y.Name
	})

	a.linkExternals()
	a.linkEndpoints()
	a.linkEnv()

	// Entry points.
	seenEP := map[EntryPoint]bool{}
	for _, f := range a.files {
		a.entries = append(a.entries, f.entries...)
	}
	for _, e := range a.entries {
		if !seenEP[e] {
			seenEP[e] = true
			r.EntryPoints = append(r.EntryPoints, e)
		}
	}
	sort.Slice(r.EntryPoints, func(i, j int) bool {
		x, y := r.EntryPoints[i], r.EntryPoints[j]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.Detail < y.Detail
	})
	entryPaths := map[string]bool{}
	for _, e := range r.EntryPoints {
		entryPaths[e.Path] = true
	}

	// Files.
	r.Files = make([]File, len(a.files))
	goErr, otherErr, tooBig := 0, map[string]int{}, 0
	for i, f := range a.files {
		r.Files[i] = File{Path: f.rel, Dir: f.dir, Lang: f.lang, Lines: f.lines, Code: f.code, Bytes: f.size,
			Package: f.pkg, Imports: imports[i], ImportedBy: importedBy[i], Symbols: len(f.syms), Todos: f.todos,
			Test: f.test, Generated: f.generated, Parsed: f.parsed}
		if f.parseErr {
			if f.lang == "Go" {
				goErr++
			} else {
				otherErr[f.lang]++
			}
		}
		if f.tooBig {
			tooBig++
		}
		if importLang(f.lang) && f.parsed && importedBy[i] == 0 && !f.test && !f.generated && !entryPaths[f.rel] &&
			len(f.endpoints) == 0 && !conventionalEntry(f) {
			r.Stats.Orphans++
		}
	}
	if goErr > 0 {
		a.warnf("%d Go %s could not be parsed", goErr, plural(goErr, "file", "files"))
	}
	for _, l := range sortedKeys(otherErr) {
		a.warnf("%d %s %s could not be read", otherErr[l], l, plural(otherErr[l], "file", "files"))
	}
	if tooBig > 0 {
		a.warnf("%d %s larger than %s were counted but not read", tooBig, plural(tooBig, "file", "files"), humanBytes(a.opt.MaxFileBytes))
	}

	a.linkDirs()

	// Languages.
	langs := map[string]*LangStat{}
	for _, f := range r.Files {
		if f.Lang == "" {
			continue
		}
		l := langs[f.Lang]
		if l == nil {
			l = &LangStat{Lang: f.Lang}
			langs[f.Lang] = l
		}
		l.Files++
		l.Lines += f.Lines
		l.Code += f.Code
	}
	for _, l := range langs {
		r.Languages = append(r.Languages, *l)
	}
	sort.Slice(r.Languages, func(i, j int) bool {
		if r.Languages[i].Lines != r.Languages[j].Lines {
			return r.Languages[i].Lines > r.Languages[j].Lines
		}
		return r.Languages[i].Lang < r.Languages[j].Lang
	})

	// Tables, services, specs.
	for _, k := range sortedKeys(a.tables) {
		r.Tables = append(r.Tables, *a.tables[k])
	}
	sort.SliceStable(r.Tables, func(i, j int) bool { return r.Tables[i].Name < r.Tables[j].Name })
	r.Services = a.services
	sort.SliceStable(r.Services, func(i, j int) bool {
		if r.Services[i].Name != r.Services[j].Name {
			return r.Services[i].Name < r.Services[j].Name
		}
		return r.Services[i].File < r.Services[j].File
	})
	r.SpecFiles = a.specFiles
	sort.Slice(r.SpecFiles, func(i, j int) bool { return r.SpecFiles[i].Path < r.SpecFiles[j].Path })

	// Free memory held by file contents.
	for _, f := range a.files {
		f.content, f.refIdents, f.goF = nil, nil, nil
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

var conventionalStems = toSet(`index main app server cli __main__ __init__ manage setup conftest wsgi asgi settings
admin apps urls models page layout loading error not-found template default route middleware global-error
+page +layout +server +page.server +layout.server +error _app _document _error vite-env.d instrumentation
tasks celery gunicorn.conf worker sw service-worker`)

// conventionalEntry: files loaded by a framework or tool by name.
func conventionalEntry(f *fileInfo) bool {
	stem := strings.TrimSuffix(f.base, path.Ext(f.base))
	if conventionalStems[stem] {
		return true
	}
	lb := strings.ToLower(f.base)
	for _, s := range []string{".config.js", ".config.ts", ".config.mjs", ".config.cjs", ".config.mts", ".d.ts", ".stories.", ".setup.", "rc.js", "rc.cjs"} {
		if strings.Contains(lb, s) {
			return true
		}
	}
	p := "/" + f.rel
	return strings.Contains(p, "/migrations/") || strings.Contains(p, "/scripts/") || strings.Contains(p, "/bin/") ||
		strings.Contains(p, "/pages/") || strings.Contains(p, "/examples/") || strings.Contains(p, "/fixtures/") ||
		strings.HasPrefix(lb, ".") || len(f.entries) > 0
}

// linkGo resolves Go imports into edges and externals, calls, refs, entry
// points and Go mount targets.
func (a *analyzer) linkGo(addEdge func(from, to *fileInfo, names []string)) map[[2]string]int {
	calls := map[[2]string]int{}
	goDirs := map[string]bool{}
	byDir := map[string][]*fileInfo{}
	for _, f := range a.files {
		if f.goF != nil {
			goDirs[f.dir] = true
			byDir[f.dir] = append(byDir[f.dir], f)
		}
	}
	if len(byDir) == 0 {
		return calls
	}
	for dir, fs := range byDir {
		count := map[string]int{}
		for _, f := range fs {
			if !strings.HasSuffix(f.goF.pkg, "_test") {
				count[f.goF.pkg]++
			}
		}
		best, bestN := "", -1
		for _, n := range sortedKeys(count) {
			if count[n] > bestN {
				best, bestN = n, count[n]
			}
		}
		a.goPkgNames[dir] = best
	}
	top := map[string]map[string]symRef{}
	methods := map[string]map[string]symRef{}
	byName := map[string]map[string][]symRef{}
	fields := map[string]map[string]map[string]goTypeRef{}
	for dir, fs := range byDir {
		top[dir], methods[dir], byName[dir], fields[dir] = map[string]symRef{}, map[string]symRef{}, map[string][]symRef{}, map[string]map[string]goTypeRef{}
		for _, f := range fs {
			for i, s := range f.syms {
				ref := symRef{f, i}
				if s.Kind == "method" {
					k := s.Parent + "." + s.Name
					if _, dup := methods[dir][k]; !dup || !f.test {
						methods[dir][k] = ref
					}
					byName[dir][s.Name] = append(byName[dir][s.Name], ref)
					continue
				}
				if old, dup := top[dir][s.Name]; !dup || (old.f.test && !f.test) {
					top[dir][s.Name] = ref
				}
			}
			for t, m := range f.goF.fields {
				fields[dir][t] = m
			}
		}
	}
	resolve := func(ip string) (string, bool) { return a.resolveGoImport(ip, goDirs) }
	requires := a.goRequireModules()
	symID := func(r symRef) string {
		s := r.f.syms[r.i]
		if s.Parent != "" {
			return r.f.rel + "#" + s.Parent + "." + s.Name
		}
		return r.f.rel + "#" + s.Name
	}
	mainDirs := map[string]*fileInfo{}
	for _, f := range a.files {
		g := f.goF
		if g == nil {
			continue
		}
		if g.isMain {
			if _, ok := mainDirs[f.dir]; !ok {
				mainDirs[f.dir] = f
			}
		}
		am := map[string]string{}
		var internalDirs []string
		extSeen := map[string]bool{}
		for _, imp := range g.imports {
			dir, internal := resolve(imp.path)
			alias := imp.alias
			if alias == "_" || alias == "." {
				alias = ""
			} else if alias == "" {
				if internal {
					alias = a.goPkgNames[dir]
				} else {
					alias = defaultGoAlias(imp.path)
				}
			}
			if alias != "" {
				am[alias] = imp.path
			}
			if internal {
				if dir == f.dir {
					continue
				}
				internalDirs = append(internalDirs, dir)
				// Names used from that package, grouped by defining file.
				namesFor := map[*fileInfo][]string{}
				for sel := range g.selRefs {
					if sel[0] == alias && alias != "" {
						if ref, ok := top[dir][sel[1]]; ok {
							namesFor[ref.f] = append(namesFor[ref.f], sel[1])
						}
					}
				}
				n := 0
				for _, t := range byDir[dir] {
					if t.test || strings.HasSuffix(t.goF.pkg, "_test") {
						continue
					}
					if n >= 50 {
						break
					}
					n++
					addEdge(f, t, namesFor[t])
				}
				continue
			}
			if isGoStd(imp.path) {
				continue
			}
			mod := goModuleFor(imp.path, requires)
			if !extSeen[mod] {
				extSeen[mod] = true
				f.externals = append(f.externals, extRef{"go", mod})
			}
		}
		// Calls.
		for _, rc := range g.calls {
			var to symRef
			ok := false
			switch {
			case rc.hasTyp:
				typeDir := f.dir
				if rc.typ.qual != "" {
					if typeDir, ok = resolve(am[rc.typ.qual]); !ok {
						continue
					}
				}
				tname := rc.typ.name
				if rc.field != "" {
					ft, found := fields[typeDir][tname][rc.field]
					if !found {
						continue
					}
					if ft.qual != "" {
						if typeDir, ok = resolve(ft.qual); !ok {
							continue
						}
					}
					tname = ft.name
				}
				to, ok = methods[typeDir][tname+"."+rc.name]
			case rc.qual != "":
				if dir, found := resolve(am[rc.qual]); found {
					to, ok = top[dir][rc.name]
				}
			default:
				to, ok = top[f.dir][rc.name]
			}
			if !ok {
				continue
			}
			if k := to.f.syms[to.i].Kind; k != "func" && k != "method" {
				continue
			}
			calls[[2]string{f.rel + "#" + rc.from, symID(to)}]++
		}
		// Refs: distinct other files that use a symbol.
		seen := map[symRef]bool{}
		credit := func(r symRef) {
			if r.f != f && !seen[r] {
				seen[r] = true
				r.f.syms[r.i].Refs++
			}
		}
		for id := range g.idents {
			if r, ok := top[f.dir][id]; ok {
				credit(r)
			}
		}
		// Methods: x.Name selectors, in this package and imported ones.
		for id := range g.sels {
			for _, r := range byName[f.dir][id] {
				credit(r)
			}
			for _, d := range internalDirs {
				for _, r := range byName[d][id] {
					credit(r)
				}
			}
		}
		for sel := range g.selRefs {
			if dir, ok := resolve(am[sel[0]]); ok {
				if r, ok := top[dir][sel[1]]; ok {
					credit(r)
				}
			}
		}
		// Go mount targets.
		for i, m := range f.mounts {
			if m.goFunc == "" {
				continue
			}
			dir := f.dir
			if m.goQual != "" {
				d, ok := resolve(am[m.goQual])
				if !ok {
					f.mounts[i].goFunc = ""
					continue
				}
				dir = d
			}
			f.mounts[i].child = "go:" + dir + ":" + m.goFunc
		}
	}
	for dir, f := range mainDirs {
		p := dir
		if p == "" {
			p = f.rel
		}
		name := path.Base(dir)
		if dir == "" {
			name = a.res.Name
		}
		a.entries = append(a.entries, EntryPoint{Path: p, Kind: "Go program", Detail: name})
	}
	return calls
}

// goModuleFor maps an import path to its module (from go.mod requires).
func goModuleFor(ip string, requires []string) string {
	for _, r := range requires {
		if ip == r || strings.HasPrefix(ip, r+"/") {
			return r
		}
	}
	parts := strings.Split(ip, "/")
	switch parts[0] {
	case "github.com", "gitlab.com", "bitbucket.org", "codeberg.org":
		if len(parts) >= 3 {
			return strings.Join(parts[:3], "/")
		}
	case "golang.org", "google.golang.org", "gopkg.in":
		if parts[0] == "golang.org" && len(parts) >= 3 {
			return strings.Join(parts[:3], "/")
		}
		if len(parts) >= 2 {
			return strings.Join(parts[:2], "/")
		}
	}
	return ip
}

// linkJava resolves Java/Kotlin imports against packages declared in the folder.
func (a *analyzer) linkJava() {
	byPkg := map[string][]*fileInfo{}
	byClass := map[string]*fileInfo{}
	for _, f := range a.files {
		if (f.lang == "Java" || f.lang == "Kotlin") && f.pkg != "" {
			byPkg[f.pkg] = append(byPkg[f.pkg], f)
			byClass[f.pkg+"."+strings.TrimSuffix(f.base, path.Ext(f.base))] = f
		}
	}
	if len(byPkg) == 0 {
		return
	}
	for _, f := range a.files {
		if f.lang != "Java" && f.lang != "Kotlin" {
			continue
		}
		var extra []rawImport
		for i, im := range f.imports {
			spec := im.spec
			if strings.HasSuffix(spec, ".*") {
				fs := byPkg[strings.TrimSuffix(spec, ".*")]
				for k, t := range fs {
					if k >= 50 {
						break
					}
					extra = append(extra, rawImport{spec: spec, line: im.line, target: t.rel})
				}
				continue
			}
			for s := spec; s != ""; {
				if t, ok := byClass[s]; ok {
					f.imports[i].target = t.rel
					f.imports[i].names = []string{spec[strings.LastIndex(spec, ".")+1:]}
					break
				}
				j := strings.LastIndex(s, ".")
				if j < 0 {
					break
				}
				s = s[:j]
			}
		}
		f.imports = append(f.imports, extra...)
	}
}

// nonGoRefs counts, for exported non-Go symbols, the importing files that
// mention the symbol (exact when the import names it). Bounded work.
func (a *analyzer) nonGoRefs(importers [][]int) {
	budget := 5_000_000
	for _, f := range a.files {
		if f.lang == "Go" || len(f.syms) == 0 || len(importers[f.idx]) == 0 {
			continue
		}
		for si := range f.syms {
			s := &f.syms[si]
			if !s.Exported {
				continue
			}
			for _, ei := range importers[f.idx] {
				if budget <= 0 {
					return
				}
				budget--
				e := a.res.Edges[ei]
				exact := len(e.Names) > 0
				found := false
				for _, n := range e.Names {
					if n == "default" {
						exact = false
					}
					if n == s.Name {
						found = true
					}
				}
				if !found && !exact {
					ids := a.byPath[e.From].refIdents
					j := sort.SearchStrings(ids, s.Name)
					found = j < len(ids) && ids[j] == s.Name
				}
				if found {
					s.Refs++
				}
			}
		}
	}
}

// linkExternals merges declared and imported packages.
func (a *analyzer) linkExternals() {
	files := map[string]map[int]bool{}
	names := map[string]extRef{}
	local := map[string]bool{}
	for _, c := range a.localCrates {
		local[extKey("cargo", c)] = true
	}
	for _, f := range a.files {
		for _, e := range f.externals {
			k := extKey(e.eco, e.name)
			if files[k] == nil {
				files[k] = map[int]bool{}
				names[k] = e
			}
			files[k][f.idx] = true
		}
	}
	var out []External
	for k, d := range a.extDeclared {
		e := *d
		e.Files = len(files[k])
		out = append(out, e)
	}
	for k, e := range names {
		if _, ok := a.extDeclared[k]; ok || local[k] {
			continue
		}
		if e.eco == "cargo" || e.eco == "gem" {
			continue // only manifest-declared crates/gems count (use paths look alike)
		}
		out = append(out, External{Name: e.name, Ecosystem: e.eco, Files: len(files[k])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Ecosystem < out[j].Ecosystem
	})
	a.res.Externals = out
}

// linkEndpoints applies mount prefixes and normalises every endpoint.
func (a *analyzer) linkEndpoints() {
	type inEdge struct {
		parent, prefix string
	}
	incoming := map[string][]inEdge{}
	isParent := map[string]bool{}
	hasEP := map[string]bool{}
	type explicitMount struct {
		f     *fileInfo
		m     mountRaw
		child string
	}
	var explicit []explicitMount
	for _, f := range a.files {
		for _, ep := range f.endpoints {
			if ep.node != "" {
				hasEP[ep.node] = true
			}
		}
	}
	for _, f := range a.files {
		for _, m := range f.mounts {
			child := m.child
			if child == "" {
				switch {
				case m.bind != "" && strings.HasPrefix(m.parent, "js:"):
					if b, ok := f.bindings[m.bind]; ok && b.imp < len(f.imports) && f.imports[b.imp].target != "" {
						child = "js:" + f.imports[b.imp].target
					}
				case strings.HasPrefix(m.parent, "py:"):
					child = a.pyMountChild(f, m)
				}
			}
			if m.explicit {
				explicit = append(explicit, explicitMount{f, m, child})
			}
			if child == "" || child == m.parent {
				continue
			}
			incoming[child] = append(incoming[child], inEdge{m.parent, m.prefix})
			isParent[m.parent] = true
		}
	}
	memo := map[string][]string{}
	visiting := map[string]bool{}
	var prefixes func(n string) []string
	prefixes = func(n string) []string {
		if v, ok := memo[n]; ok {
			return v
		}
		in := incoming[n]
		if len(in) == 0 || visiting[n] {
			return []string{""}
		}
		visiting[n] = true
		var out []string
		seen := map[string]bool{}
		for _, e := range in {
			for _, p := range prefixes(e.parent) {
				full := joinPath(p, e.prefix)
				if !seen[full] && len(out) < 8 {
					seen[full] = true
					out = append(out, full)
				}
			}
		}
		visiting[n] = false
		memo[n] = out
		return out
	}
	seen := map[string]bool{}
	add := func(ep Endpoint) {
		ep.Path = normPath(ep.Path)
		ep.Params = pathParams(ep.Path)
		if len(ep.Params) == 0 {
			ep.Params = nil
		}
		if ep.Method == "" {
			ep.Method = "ANY"
		}
		k := ep.Method + " " + ep.Path + " " + ep.File + " " + fmt.Sprint(ep.Line) + " " + ep.Handler
		if seen[k] {
			return
		}
		seen[k] = true
		a.res.Endpoints = append(a.res.Endpoints, ep)
	}
	for _, f := range a.files {
		for _, ep := range f.endpoints {
			if f.test && ep.Source == "code" {
				continue // mock servers in tests are not the app's API
			}
			if ep.node == "" {
				add(ep.Endpoint)
				continue
			}
			for _, p := range prefixes(ep.node) {
				e := ep.Endpoint
				e.Path = joinPath(p, e.Path)
				add(e)
			}
		}
	}
	// An explicit Mount of something without routes (a file server, a
	// third-party handler) is one catch-all endpoint.
	for _, x := range explicit {
		if x.child != "" && (hasEP[x.child] || isParent[x.child]) {
			continue
		}
		for _, p := range prefixes(x.m.parent) {
			add(Endpoint{Method: "ANY", Path: joinPath(joinPath(p, x.m.prefix), "/*"), Handler: x.m.handler, File: x.f.rel,
				Line: x.m.line, Framework: x.m.fw, Source: "code"})
		}
	}
	sort.SliceStable(a.res.Endpoints, func(i, j int) bool {
		x, y := a.res.Endpoints[i], a.res.Endpoints[j]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		if x.Method != y.Method {
			return x.Method < y.Method
		}
		if x.File != y.File {
			return x.File < y.File
		}
		return x.Line < y.Line
	})
}

// linkEnv merges env reads with what docs and examples document.
func (a *analyzer) linkEnv() {
	type use struct {
		path string
		line int
	}
	uses := map[string][]use{}
	defs := map[string]string{}
	for _, f := range a.files {
		for _, e := range f.envs {
			if e.name == "" {
				continue
			}
			uses[e.name] = append(uses[e.name], use{f.rel, e.line})
			if defs[e.name] == "" && e.def != "" {
				defs[e.name] = e.def
			}
		}
	}
	names := map[string]bool{}
	for n := range uses {
		names[n] = true
	}
	for n := range a.envDocOnly {
		names[n] = true
	}
	for _, n := range sortedKeys(names) {
		us := uses[n]
		sort.Slice(us, func(i, j int) bool {
			if us[i].path != us[j].path {
				return us[i].path < us[j].path
			}
			return us[i].line < us[j].line
		})
		ev := EnvVar{Name: n, Uses: []string{}, Default: clip(defs[n], 200),
			Documented: a.envDocOnly[n] || a.envDocs[n] || a.composeEnv[n]}
		last := ""
		for _, u := range us {
			s := fmt.Sprintf("%s:%d", u.path, u.line)
			if s == last {
				continue
			}
			last = s
			if len(ev.Uses) >= 20 {
				break
			}
			ev.Uses = append(ev.Uses, s)
		}
		a.res.EnvVars = append(a.res.EnvVars, ev)
	}
}

// linkDirs rolls files up into folders and finds folder cycles.
func (a *analyzer) linkDirs() {
	r := a.res
	direct := map[string]int{}
	lines := map[string]int{}
	langLines := map[string]map[string]int{}
	for _, f := range r.Files {
		direct[f.Dir]++
		for d := f.Dir; ; d = dirOf(d) {
			lines[d] += f.Lines
			if langLines[d] == nil {
				langLines[d] = map[string]int{}
			}
			if f.Lang != "" {
				langLines[d][f.Lang] += f.Lines
			}
			if d == "" {
				break
			}
		}
	}
	if len(r.Files) == 0 {
		return
	}
	graph := map[string]map[string]bool{}
	usedBy := map[string]map[string]bool{}
	for _, e := range r.Edges {
		fd, td := dirOf(e.From), dirOf(e.To)
		if fd == td {
			continue
		}
		if graph[fd] == nil {
			graph[fd] = map[string]bool{}
		}
		graph[fd][td] = true
		if usedBy[td] == nil {
			usedBy[td] = map[string]bool{}
		}
		usedBy[td][fd] = true
	}
	for _, d := range sortedKeys(lines) {
		best, bestN := "", -1
		for _, l := range sortedKeys(langLines[d]) {
			if langLines[d][l] > bestN {
				best, bestN = l, langLines[d][l]
			}
		}
		r.Dirs = append(r.Dirs, Dir{Path: d, Files: direct[d], Lines: lines[d], Lang: best, DependsOn: len(graph[d]), UsedBy: len(usedBy[d])})
	}
	r.Cycles = tarjanCycles(graph, 50)
}

// tarjanCycles returns strongly connected components with two or more
// folders, each sorted, the list sorted, at most limit.
func tarjanCycles(graph map[string]map[string]bool, limit int) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	next := 0
	var strong func(v string)
	strong = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range sortedKeys(graph[v]) {
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) >= 2 {
				sort.Strings(comp)
				out = append(out, comp)
			}
		}
	}
	for _, v := range sortedKeys(graph) {
		if _, seen := index[v]; !seen {
			strong(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.Join(out[i], "\x00") < strings.Join(out[j], "\x00") })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
