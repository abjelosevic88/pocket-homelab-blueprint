package analyze

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// alwaysSkip are folders that hold tooling state or build output.
var alwaysSkip = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "dist": true, "build": true, "target": true,
	"out": true, ".next": true, ".nuxt": true, ".output": true, ".svelte-kit": true,
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true, ".idea": true,
	".vscode": true, "coverage": true, ".cache": true, "obj": true, ".turbo": true,
	".parcel-cache": true, ".gradle": true, ".mypy_cache": true, ".pytest_cache": true,
	".ruff_cache": true, ".terraform": true, ".yarn": true, ".pnpm-store": true,
	".angular": true, ".dart_tool": true, ".expo": true, "storybook-static": true,
	".eggs": true, "site-packages": true, ".nyc_output": true, ".docusaurus": true,
	".vercel": true, ".serverless": true, ".direnv": true, "htmlcov": true,
}

// vendoredDirs are third-party code kept in the tree; read only with Options.Vendored.
var vendoredDirs = map[string]bool{
	"vendor": true, "third_party": true, "node_modules": true, "bower_components": true,
	"jspm_packages": true, "Pods": true,
}

// binaryExt are extensions that are never text; skipped without reading.
var binaryExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".bmp": true, ".tiff": true, ".avif": true, ".heic": true, ".psd": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true,
	".zst": true, ".7z": true, ".rar": true, ".tar": true, ".jar": true, ".war": true,
	".class": true, ".pyc": true, ".pyo": true, ".o": true, ".a": true, ".so": true,
	".dylib": true, ".dll": true, ".exe": true, ".bin": true, ".wasm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp3": true, ".mp4": true, ".m4a": true, ".flac": true, ".wav": true, ".ogg": true,
	".webm": true, ".mov": true, ".avi": true, ".mkv": true, ".sqlite": true,
	".sqlite3": true, ".db": true, ".DS_Store": true, ".lockb": true, ".parquet": true,
	".keystore": true, ".jks": true, ".p12": true, ".pfx": true, ".der": true,
}

// walk lists the files under the root (without reading them).
func (a *analyzer) walk() error {
	ign := loadGitignore(filepath.Join(a.root, ".gitignore"))
	count := 0
	a.progress("files", 0, 0)
	err := filepath.WalkDir(a.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != a.root {
				return fs.SkipDir
			}
			return nil
		}
		if cerr := a.ctx.Err(); cerr != nil {
			return cerr
		}
		if p == a.root {
			return nil
		}
		relOS, rerr := filepath.Rel(a.root, p)
		if rerr != nil {
			return nil
		}
		rel := filepath.ToSlash(relOS)
		name := d.Name()
		if d.IsDir() {
			if alwaysSkip[name] || (!a.opt.Vendored && vendoredDirs[name]) || strings.HasSuffix(name, ".egg-info") {
				a.skippedDirs[name]++
				return fs.SkipDir
			}
			if name == "bin" && isDotNetBin(filepath.Dir(p)) {
				a.skippedDirs[name]++
				return fs.SkipDir
			}
			if ign.match(rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if ign.match(rel, false) {
			a.skipped++
			return nil
		}
		typ := d.Type()
		var size int64
		target := p
		if typ&fs.ModeSymlink != 0 {
			// Follow a file symlink only when it stays inside the root.
			t, err := filepath.EvalSymlinks(p)
			if err != nil || !within(a.root, t) {
				a.skipped++
				return nil
			}
			st, err := os.Stat(t)
			if err != nil || !st.Mode().IsRegular() {
				a.skipped++
				return nil
			}
			size = st.Size()
			target = t
		} else if !typ.IsRegular() {
			return nil
		} else {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			size = info.Size()
		}
		ext := strings.ToLower(path.Ext(name))
		if binaryExt[ext] || name == ".DS_Store" {
			a.skipped++
			return nil
		}
		if count >= a.opt.MaxFiles {
			a.limitHit = true
			a.skipped++
			return nil
		}
		count++
		f := &fileInfo{rel: rel, dir: dirOf(rel), base: name, abs: target, size: size}
		f.lang = detectLang(name, ext)
		a.files = append(a.files, f)
		if count%500 == 0 {
			a.progress("files", count, 0)
		}
		return nil
	})
	if err != nil && a.ctx.Err() != nil {
		return a.ctx.Err()
	}
	a.progress("files", count, count)
	if a.limitHit {
		a.warnf("Only the first %d files were read; the rest were left out", a.opt.MaxFiles)
	}
	if len(a.skippedDirs) > 0 {
		names := sortedKeys(a.skippedDirs)
		if len(names) > 6 {
			names = append(names[:6], "…")
		}
		total := 0
		for _, n := range a.skippedDirs {
			total += n
		}
		a.warnf("Left out %d dependency or build folders (%s)", total, strings.Join(names, ", "))
	}
	return nil
}

// isDotNetBin reports whether a "bin" folder sits next to a .NET project.
func isDotNetBin(parent string) bool {
	if st, err := os.Stat(filepath.Join(parent, "obj")); err == nil && st.IsDir() {
		return true
	}
	ents, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if strings.HasSuffix(n, ".csproj") || strings.HasSuffix(n, ".fsproj") || strings.HasSuffix(n, ".vbproj") || strings.HasSuffix(n, ".sln") {
			return true
		}
	}
	return false
}

func within(root, p string) bool {
	if p == root {
		return true
	}
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// readFile reads one file, drops binaries, and counts lines.
func (a *analyzer) readFile(f *fileInfo) {
	f.read = true
	fh, err := os.Open(f.abs)
	if err != nil {
		f.binary = true // unreadable: treat as left out
		return
	}
	defer fh.Close()
	if f.size > a.opt.MaxFileBytes {
		f.tooBig = true
		head := make([]byte, 8192)
		n, _ := io.ReadFull(fh, head)
		if bytes.IndexByte(head[:n], 0) >= 0 {
			f.binary = true
			return
		}
		if f.size <= 32<<20 {
			// Counting newlines is cheap even for big files.
			_, _ = fh.Seek(0, io.SeekStart)
			br := bufio.NewReaderSize(fh, 64<<10)
			buf := make([]byte, 64<<10)
			lines, last := 0, byte('\n')
			for {
				n, err := br.Read(buf)
				lines += bytes.Count(buf[:n], []byte{'\n'})
				if n > 0 {
					last = buf[n-1]
				}
				if err != nil {
					break
				}
			}
			if last != '\n' {
				lines++
			}
			f.lines = lines
		}
		f.generated = isGeneratedName(f.rel, f.base) || isGeneratedHeader(head[:n])
		f.test = isTestPath(f.rel, f.base)
		return
	}
	data, err := io.ReadAll(io.LimitReader(fh, a.opt.MaxFileBytes+1))
	if err != nil {
		f.binary = true
		return
	}
	sniff := data
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		f.binary = true
		return
	}
	f.size = int64(len(data))
	f.content = data
	f.test = isTestPath(f.rel, f.base)
	f.generated = isGeneratedName(f.rel, f.base) || isGeneratedHeader(sniff)
	f.lines, f.code, f.todos = countLines(data, familyFor(f.base, f.lang))
	if f.lines > 0 && len(data) > 20000 && len(data)/f.lines > 300 {
		switch f.lang {
		case "JavaScript", "TypeScript", "CSS", "SCSS", "HTML":
			f.minified, f.generated = true, true
		}
	}
	if f.lang != "" && !f.minified && hasParser(f.lang) {
		f.parsed = true
	}
}

// --- .gitignore ---

type ignoreRule struct {
	re      *regexp.Regexp
	literal string // fast path: no glob characters
	anchor  bool
	negate  bool
	dirOnly bool
}

type gitignore struct{ rules []ignoreRule }

func loadGitignore(p string) *gitignore {
	g := &gitignore{}
	data, err := os.ReadFile(p)
	if err != nil {
		return g
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimRight(line, " \t")
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if !strings.ContainsAny(line, "*?[\\") {
			r.literal, r.anchor = line, anchored
			g.rules = append(g.rules, r)
			continue
		}
		expr := globToRegexp(line)
		if anchored {
			expr = "^" + expr + "$"
		} else {
			expr = "(^|/)" + expr + "$"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			continue
		}
		r.re = re
		g.rules = append(g.rules, r)
	}
	return g
}

// match reports whether rel (slash path) is ignored. Last matching rule wins.
func (g *gitignore) match(rel string, isDir bool) bool {
	ignored := false
	for _, r := range g.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.literal != "" {
			hit := rel == r.literal
			if !hit && !r.anchor {
				hit = rel == r.literal || strings.HasSuffix(rel, "/"+r.literal)
			}
			if hit {
				ignored = !r.negate
			}
			continue
		}
		if r.re.MatchString(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

// globToRegexp converts a gitignore glob to a regular expression body.
func globToRegexp(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				// "**/" = any number of folders, "/**" = everything below.
				if i+2 < len(g) && g[i+2] == '/' {
					b.WriteString("(.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(g[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			cls := g[i+1 : i+j]
			if strings.HasPrefix(cls, "!") {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(cls, `\`, `\\`) + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// sortFiles orders files by path (WalkDir is already lexical per folder,
// but symlink targets and sorting by full path keep it stable).
func sortFiles(fs []*fileInfo) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].rel < fs[j].rel })
}
