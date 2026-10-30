package analyze

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// pyIndex maps dotted module names to files.
type pyIndex struct {
	mods     map[string][]string // module → files (one per source root that holds it)
	localTop map[string]bool     // top-level names that exist in the folder
	modName  map[string]string   // file → its module name (deepest root)
}

var pyStdlib = toSet(`__future__ _thread abc aifc argparse array ast asynchat asyncio asyncore atexit audioop base64 bdb
binascii bisect builtins bz2 calendar cgi cgitb chunk cmath cmd code codecs codeop collections colorsys compileall
concurrent configparser contextlib contextvars copy copyreg cProfile crypt csv ctypes curses dataclasses datetime dbm
decimal difflib dis distutils doctest email encodings ensurepip enum errno faulthandler fcntl filecmp fileinput fnmatch
fractions ftplib functools gc getopt getpass gettext glob graphlib grp gzip hashlib heapq hmac html http idlelib imaplib
imghdr imp importlib inspect io ipaddress itertools json keyword lib2to3 linecache locale logging lzma mailbox mailcap
marshal math mimetypes mmap modulefinder msvcrt multiprocessing netrc nis nntplib numbers operator optparse os
ossaudiodev pathlib pdb pickle pickletools pipes pkgutil platform plistlib poplib posix posixpath pprint profile pstats
pty pwd py_compile pyclbr pydoc queue quopri random re readline reprlib resource rlcompleter runpy sched secrets select
selectors shelve shlex shutil signal site smtpd smtplib sndhdr socket socketserver spwd sqlite3 ssl stat statistics
string stringprep struct subprocess sunau symtable sys sysconfig syslog tabnanny tarfile telnetlib tempfile termios
textwrap threading time timeit tkinter token tokenize tomllib trace traceback tracemalloc tty turtle types typing
unicodedata unittest urllib uu uuid venv warnings wave weakref webbrowser winreg winsound wsgiref xdrlib xml xmlrpc
zipapp zipfile zipimport zlib zoneinfo typing_extensions ntpath genericpath sre_compile sre_parse opcode`)

// pyDist maps import names to the distribution names used in manifests.
var pyDist = map[string]string{
	"yaml": "pyyaml", "PIL": "pillow", "sklearn": "scikit-learn", "cv2": "opencv-python", "bs4": "beautifulsoup4",
	"dotenv": "python-dotenv", "jwt": "pyjwt", "dateutil": "python-dateutil", "attr": "attrs", "magic": "python-magic",
	"serial": "pyserial", "Crypto": "pycryptodome", "OpenSSL": "pyopenssl", "MySQLdb": "mysqlclient",
	"psycopg2": "psycopg2", "google": "google", "jose": "python-jose", "multipart": "python-multipart",
	"telegram": "python-telegram-bot", "docx": "python-docx", "pptx": "python-pptx", "slugify": "python-slugify",
	"skimage": "scikit-image", "Levenshtein": "python-levenshtein", "fitz": "pymupdf", "gi": "pygobject",
	"win32api": "pywin32", "usb": "pyusb", "socks": "pysocks", "zmq": "pyzmq", "git": "gitpython",
	"discord": "discord.py", "ldap": "python-ldap", "nacl": "pynacl", "lxml": "lxml", "markdown": "markdown",
}

func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// pyNorm normalises a distribution name (PEP 503).
func pyNorm(n string) string {
	n = strings.ToLower(n)
	return strings.NewReplacer("_", "-", ".", "-").Replace(n)
}

// preparePython finds source roots and indexes every module.
func (a *analyzer) preparePython() {
	idx := &pyIndex{mods: map[string][]string{}, localTop: map[string]bool{}, modName: map[string]string{}}
	a.py = idx
	pkgDirs := map[string]bool{}
	var pyFiles []*fileInfo
	for _, f := range a.files {
		if f.lang != "Python" {
			continue
		}
		pyFiles = append(pyFiles, f)
		if f.base == "__init__.py" {
			pkgDirs[f.dir] = true
		}
	}
	if len(pyFiles) == 0 {
		return
	}
	roots := map[string]bool{"": true}
	for d := range pkgDirs {
		if !pkgDirs[dirOf(d)] || d == "" {
			roots[dirOf(d)] = true
		}
	}
	for _, f := range a.files {
		switch f.base {
		case "pyproject.toml", "setup.py", "setup.cfg", "manage.py":
			roots[f.dir] = true
		}
		if path.Base(f.dir) == "src" {
			roots[f.dir] = true
		}
	}
	rootList := sortedKeys(roots)
	// Deepest root first so modName picks the most specific one.
	sort.SliceStable(rootList, func(i, j int) bool { return len(rootList[i]) > len(rootList[j]) })
	for _, f := range pyFiles {
		stem := strings.TrimSuffix(strings.TrimSuffix(f.base, ".py"), ".pyi")
		idx.localTop[stem] = true
		if f.dir != "" {
			idx.localTop[path.Base(f.dir)] = true
		}
		for _, r := range rootList {
			if r != "" && !strings.HasPrefix(f.rel, r+"/") {
				continue
			}
			rel := f.rel
			if r != "" {
				rel = f.rel[len(r)+1:]
			}
			mod := strings.TrimSuffix(strings.TrimSuffix(rel, ".py"), ".pyi")
			mod = strings.TrimSuffix(mod, "/__init__")
			if mod == "__init__" || mod == "" {
				continue
			}
			mod = strings.ReplaceAll(mod, "/", ".")
			idx.mods[mod] = append(idx.mods[mod], f.rel)
			if _, ok := idx.modName[f.rel]; !ok {
				idx.modName[f.rel] = mod
			}
		}
	}
}

// pyResolve resolves a module (level = leading dots) from file f.
func (a *analyzer) pyResolve(f *fileInfo, level int, mod string) string {
	if level > 0 {
		base := f.dir
		for i := 1; i < level; i++ {
			if base == "" {
				return ""
			}
			base = dirOf(base)
		}
		p := base
		if mod != "" {
			p = path.Join(base, strings.ReplaceAll(mod, ".", "/"))
		}
		return a.pyFileFor(p)
	}
	if mod == "" {
		return ""
	}
	// A sibling module (the script's folder is on sys.path).
	if r := a.pyFileFor(path.Join(f.dir, strings.ReplaceAll(mod, ".", "/"))); r != "" {
		return r
	}
	cands := a.py.mods[mod]
	switch len(cands) {
	case 0:
		return ""
	case 1:
		return cands[0]
	}
	best, bestN := cands[0], -1
	for _, c := range cands {
		n := commonPrefix(c, f.rel)
		if n > bestN {
			best, bestN = c, n
		}
	}
	return best
}

func (a *analyzer) pyFileFor(p string) string {
	p = cleanRel(p)
	if strings.HasPrefix(p, "..") {
		return ""
	}
	for _, c := range []string{p + ".py", p + "/__init__.py", p + ".pyi"} {
		if a.byPath[c] != nil {
			return c
		}
	}
	return ""
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// pyClean blanks comments and the inside of triple-quoted strings (a ""
// placeholder stays), keeping line numbers.
func pyClean(src string) string {
	b := []byte(src)
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		switch {
		case c == '#':
			for i < n && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case c == '"' || c == '\'':
			if i+2 < n && b[i+1] == c && b[i+2] == c {
				j := i + 3
				for j < n {
					if b[j] == '\\' {
						j += 2
						continue
					}
					if j+2 < n && b[j] == c && b[j+1] == c && b[j+2] == c {
						break
					}
					j++
				}
				end := j + 3
				if end > n {
					end = n
				}
				for k := i + 2; k < end; k++ {
					if b[k] != '\n' {
						b[k] = ' '
					}
				}
				b[i], b[i+1] = '"', '"'
				i = end
				continue
			}
			j := i + 1
			for j < n && b[j] != c && b[j] != '\n' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
		default:
			i++
		}
	}
	return string(b)
}

var (
	pyDefRe       = regexp.MustCompile(`^(\s*)(async\s+)?def\s+([A-Za-z_]\w*)\s*[\[(]`)
	pyClassRe     = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)\s*([(:\[])`)
	pyConstRe     = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)\s*(:[^=]+)?=[^=]`)
	pyImportRe    = regexp.MustCompile(`^import\s+(.+)$`)
	pyFromRe      = regexp.MustCompile(`^from\s+(\.*)([\w.]*)\s+import\s+(.+)$`)
	pyRouterRe    = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[\w.]+\s*)?=\s*(?:[\w.]+\.)?(FastAPI|APIRouter|Flask|Blueprint|Quart|Sanic)\s*\((.*)\)`)
	pyPrefixRe    = regexp.MustCompile(`\b(?:prefix|url_prefix)\s*=\s*[rf]?['"]([^'"]*)['"]`)
	pyRouteDecoRe = regexp.MustCompile(`^@([A-Za-z_][\w.]*)\.(get|post|put|patch|delete|head|options|route|api_route|websocket)\s*\((.*)\)\s*$`)
	pyMethodsRe   = regexp.MustCompile(`\bmethods\s*=\s*[\[(]([^\])]*)[\])]`)
	pyQuotedRe    = regexp.MustCompile(`['"]([^'"]*)['"]`)
	pyIncludeRe   = regexp.MustCompile(`\b([A-Za-z_]\w*)\.(include_router|register_blueprint)\s*\(\s*([A-Za-z_][\w.]*)\s*(.*)$`)
	pyMountRe     = regexp.MustCompile(`\b([A-Za-z_]\w*)\.mount\s*\(\s*[rf]?['"]([^'"]*)['"]\s*,\s*([A-Za-z_][\w.]*)`)
	pyDjangoRe    = regexp.MustCompile(`\b(re_path|path|url)\s*\(`)
	pyNamedGrpRe  = regexp.MustCompile(`\(\?P<([A-Za-z_]\w*)>[^)]*\)`)
	pyEnvIdxRe    = regexp.MustCompile(`\bos\.environ\[\s*[rf]?['"]([^'"\n]+)['"]\s*\]`)
	pyEnvGetRe    = regexp.MustCompile(`\b(?:os\.environ\.get|os\.environ\.setdefault|os\.getenv|environ\.get|getenv)\(\s*['"]([^'"\n]+)['"](?:\s*,\s*(?:default\s*=\s*)?(?:['"]([^'"\n]*)['"]|([\w.\-]+)\s*[,)]))?`)
	pyEnvHelperRe = regexp.MustCompile(`\b(?:env|config|env\.\w+)\(\s*['"]([A-Z][A-Z0-9_]{2,})['"](?:\s*,\s*(?:default\s*=\s*)?(?:['"]([^'"\n]*)['"]|([\w.\-]+)\s*[,)]))?`)
	pyEnvPrefixRe = regexp.MustCompile(`env_prefix\s*=\s*['"]([^'"]*)['"]`)
	pyFieldRe     = regexp.MustCompile(`^\s+([a-z_][a-z0-9_]*)\s*:\s*[^=]+?(?:=\s*(.+))?$`)
)

type pyRouter struct{ fw, prefix string }

// parsePython reads one Python file with indentation-aware patterns.
func (a *analyzer) parsePython(f *fileInfo) {
	src := string(f.content)
	clean := pyClean(src)
	f.refText = clean
	lines := strings.Split(clean, "\n")
	raw := strings.Split(src, "\n")
	f.bindings = map[string]binding{}
	if m, ok := a.py.modName[f.rel]; ok {
		f.pkg = m
	}
	indentOf := func(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }
	// logical joins a statement continued with brackets or backslashes.
	logical := func(i int) (string, int) {
		s := strings.TrimSpace(lines[i])
		j := i
		for j+1 < len(lines) && j-i < 40 {
			open := strings.Count(s, "(") + strings.Count(s, "[") + strings.Count(s, "{")
			closed := strings.Count(s, ")") + strings.Count(s, "]") + strings.Count(s, "}")
			if open > closed || strings.HasSuffix(s, `\`) {
				j++
				s = strings.TrimSuffix(s, `\`) + " " + strings.TrimSpace(lines[j])
				continue
			}
			break
		}
		return s, j
	}

	extSeen := map[string]bool{}
	addExt := func(mod string) {
		top := strings.SplitN(mod, ".", 2)[0]
		if top == "" || pyStdlib[top] || a.py.localTop[top] || extSeen[top] {
			return
		}
		extSeen[top] = true
		name := top
		if d, ok := pyDist[top]; ok {
			name = d
		}
		f.externals = append(f.externals, extRef{"pypi", pyNorm(name)})
	}
	fw := ""
	routers := map[string]pyRouter{}
	type stackEnt struct {
		indent int
		name   string
		class  bool
	}
	var stack []stackEnt
	var decos []string
	var settings []struct {
		indent int
		prefix string
	}

	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		ind := indentOf(l)
		for len(settings) > 0 && ind <= settings[len(settings)-1].indent && !strings.HasPrefix(t, ")") {
			settings = settings[:len(settings)-1]
		}
		switch {
		case strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "from "):
			stmt, j := logical(i)
			a.pyImport(f, stmt, i+1, addExt)
			if strings.Contains(stmt, "fastapi") {
				fw = "fastapi"
			} else if strings.Contains(stmt, "flask") && fw == "" {
				fw = "flask"
			}
			i = j
			continue
		case strings.HasPrefix(t, "@"):
			stmt, j := logical(i)
			decos = append(decos, stmt)
			i = j
			continue
		case strings.HasPrefix(t, "if __name__") && strings.Contains(t, "__main__") && ind == 0:
			f.entries = append(f.entries, EntryPoint{Path: f.rel, Kind: "Python script"})
		}
		if m := pyDefRe.FindStringSubmatch(l); m != nil {
			k := len(m[1])
			for len(stack) > 0 && stack[len(stack)-1].indent >= k {
				stack = stack[:len(stack)-1]
			}
			sig, j := logical(i)
			kind, parent, keep := "func", "", true
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.class {
					kind, parent = "method", top.name
					// Only direct methods of top-level-ish classes.
				} else {
					keep = false
				}
			}
			name := m[3]
			if keep {
				s := Symbol{File: f.rel, Name: name, Kind: kind, Parent: parent, Line: i + 1, Exported: !strings.HasPrefix(name, "_") || (strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")),
					Signature: clip(strings.TrimSuffix(strings.TrimSpace(sig), ":"), 160), Doc: pyDocstring(raw, j+1)}
				if e := pyBlockEnd(lines, i, k); e > i {
					s.EndLine = e + 1
				}
				f.syms = append(f.syms, s)
				// Routes from decorators.
				for _, d := range decos {
					a.pyDecoRoute(f, d, name, i+1, routers, fw)
				}
			}
			decos = nil
			stack = append(stack, stackEnt{indent: k, name: name})
			i = j
			continue
		}
		if m := pyClassRe.FindStringSubmatch(l); m != nil {
			k := len(m[1])
			for len(stack) > 0 && stack[len(stack)-1].indent >= k {
				stack = stack[:len(stack)-1]
			}
			sig, j := logical(i)
			if len(stack) == 0 || stack[len(stack)-1].class {
				s := Symbol{File: f.rel, Name: m[2], Kind: "class", Line: i + 1, Exported: !strings.HasPrefix(m[2], "_"),
					Signature: clip(strings.TrimSuffix(strings.TrimSpace(sig), ":"), 160), Doc: pyDocstring(raw, j+1)}
				if len(stack) > 0 {
					s.Parent = stack[len(stack)-1].name
				}
				if e := pyBlockEnd(lines, i, k); e > i {
					s.EndLine = e + 1
				}
				f.syms = append(f.syms, s)
			}
			if strings.Contains(sig, "BaseSettings") {
				p := ""
				end := pyBlockEnd(lines, i, k)
				for x := i; x <= end && x < len(lines); x++ {
					if pm := pyEnvPrefixRe.FindStringSubmatch(lines[x]); pm != nil {
						p = pm[1]
					}
				}
				settings = append(settings, struct {
					indent int
					prefix string
				}{k, p})
			}
			decos = nil
			stack = append(stack, stackEnt{indent: k, name: m[2], class: true})
			i = j
			continue
		}
		decos = nil
		if len(settings) > 0 {
			st := settings[len(settings)-1]
			if fm := pyFieldRe.FindStringSubmatch(l); fm != nil && fm[1] != "model_config" && !strings.HasPrefix(fm[1], "_") {
				e := envUse{name: strings.ToUpper(st.prefix + fm[1]), line: i + 1}
				if fm[2] != "" {
					if s, ok := strLit(strings.TrimSpace(raw[i][strings.Index(raw[i], "=")+1:])); ok {
						e.def = s
					} else if v := strings.TrimSpace(fm[2]); len(v) < 40 && !strings.ContainsAny(v, "()") {
						e.def = v
					}
				}
				f.envs = append(f.envs, e)
			}
		}
		if ind == 0 {
			if m := pyConstRe.FindStringSubmatch(l); m != nil {
				f.syms = append(f.syms, Symbol{File: f.rel, Name: m[1], Kind: "const", Line: i + 1, Exported: true,
					Signature: clip(strings.TrimSpace(raw[i]), 160)})
			}
			if strings.Contains(t, "(") && (strings.Contains(t, "FastAPI") || strings.Contains(t, "Router") || strings.Contains(t, "Flask") ||
				strings.Contains(t, "Blueprint") || strings.Contains(t, "Quart") || strings.Contains(t, "Sanic")) {
				stmt, _ := logical(i)
				if m := pyRouterRe.FindStringSubmatch(stmt); m != nil {
					r := pyRouter{fw: "fastapi"}
					switch m[2] {
					case "Flask", "Blueprint", "Quart":
						r.fw = "flask"
					case "Sanic":
						r.fw = "sanic"
					}
					if pm := pyPrefixRe.FindStringSubmatch(m[3]); pm != nil {
						r.prefix = pm[1]
					}
					routers[m[1]] = r
				}
			}
		}
		if strings.Contains(t, "include_router") || strings.Contains(t, "register_blueprint") || strings.Contains(t, ".mount(") {
			stmt, _ := logical(i)
			a.pyMount(f, stmt, i+1, routers)
		}
	}

	if f.base == "urls.py" || strings.Contains(clean, "urlpatterns") {
		a.djangoRoutes(f, clean)
	}

	// Env reads.
	li := newLineIndex(clean)
	for i, re := range []*regexp.Regexp{pyEnvIdxRe, pyEnvGetRe, pyEnvHelperRe} {
		hint := []string{"environ[", "env", "env("}[i]
		if i == 2 && !strings.Contains(clean, "config(") && !strings.Contains(clean, "env(") && !strings.Contains(clean, "env.") {
			continue
		}
		if !strings.Contains(clean, hint) {
			continue
		}
		for _, m := range re.FindAllStringSubmatchIndex(clean, -1) {
			e := envUse{name: clean[m[2]:m[3]], line: li.line(m[0])}
			if len(m) > 4 && m[4] >= 0 {
				e.def = clean[m[4]:m[5]]
			} else if len(m) > 6 && m[6] >= 0 && clean[m[6]:m[7]] != "None" {
				e.def = clean[m[6]:m[7]]
			}
			if looseEnvRe.MatchString(e.name) {
				f.envs = append(f.envs, e)
			}
		}
	}
}

// pyBlockEnd returns the last line (0-based) of the block opened at line i
// with indent k.
func pyBlockEnd(lines []string, i, k int) int {
	end := i
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		ind := len(lines[j]) - len(strings.TrimLeft(lines[j], " \t"))
		if ind <= k && !strings.HasPrefix(t, ")") && !strings.HasPrefix(t, "]") && !strings.HasPrefix(t, "}") {
			break
		}
		end = j
	}
	return end
}

// pyDocstring reads a docstring that starts on line i (0-based).
func pyDocstring(raw []string, i int) string {
	for ; i < len(raw) && strings.TrimSpace(raw[i]) == ""; i++ {
	}
	if i >= len(raw) {
		return ""
	}
	t := strings.TrimSpace(raw[i])
	t = strings.TrimLeft(t, "rRuU")
	q := ""
	switch {
	case strings.HasPrefix(t, `"""`):
		q = `"""`
	case strings.HasPrefix(t, `'''`):
		q = `'''`
	default:
		return ""
	}
	body := t[3:]
	if j := strings.Index(body, q); j >= 0 {
		return firstSentence(body[:j])
	}
	parts := []string{body}
	for k := i + 1; k < len(raw) && k < i+30; k++ {
		if j := strings.Index(raw[k], q); j >= 0 {
			parts = append(parts, raw[k][:j])
			break
		}
		parts = append(parts, raw[k])
	}
	return firstSentence(strings.Join(parts, "\n"))
}

// pyImport records one import statement.
func (a *analyzer) pyImport(f *fileInfo, stmt string, line int, addExt func(string)) {
	stmt = strings.TrimSpace(strings.SplitN(stmt, ";", 2)[0])
	if m := pyImportRe.FindStringSubmatch(stmt); m != nil {
		for _, part := range strings.Split(m[1], ",") {
			fs := strings.Fields(part)
			if len(fs) == 0 {
				continue
			}
			mod := fs[0]
			target := a.pyResolve(f, 0, mod)
			f.imports = append(f.imports, rawImport{spec: mod, line: line, target: target})
			local := strings.SplitN(mod, ".", 2)[0]
			if len(fs) >= 3 && fs[1] == "as" {
				local = fs[2]
			}
			f.bindings[local] = binding{imp: len(f.imports) - 1}
			if target == "" {
				addExt(mod)
			}
		}
		return
	}
	m := pyFromRe.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	level, mod := len(m[1]), m[2]
	list := strings.Trim(strings.TrimSpace(m[3]), "()")
	var names []string
	locals := map[string]string{}
	for _, part := range strings.Split(list, ",") {
		fs := strings.Fields(part)
		if len(fs) == 0 || fs[0] == "*" {
			continue
		}
		names = append(names, fs[0])
		local := fs[0]
		if len(fs) >= 3 && fs[1] == "as" {
			local = fs[2]
		}
		locals[local] = fs[0]
	}
	base := a.pyResolve(f, level, mod)
	var baseNames []string
	for _, n := range names {
		sub := n
		if mod != "" {
			sub = mod + "." + n
		}
		if t := a.pyResolve(f, level, sub); t != "" && t != base && (level > 0 || mod != "") {
			f.imports = append(f.imports, rawImport{spec: sub, level: level, line: line, target: t, names: []string{n}})
			continue
		}
		baseNames = append(baseNames, n)
	}
	f.imports = append(f.imports, rawImport{spec: mod, level: level, line: line, target: base, names: baseNames})
	idx := len(f.imports) - 1
	for local, imported := range locals {
		f.bindings[local] = binding{imp: idx, name: imported}
	}
	if base == "" && level == 0 {
		addExt(mod)
	}
}

// pyDecoRoute turns @app.get("/x") / @bp.route("/x", methods=[…]) into endpoints.
func (a *analyzer) pyDecoRoute(f *fileInfo, deco, handler string, line int, routers map[string]pyRouter, fileFW string) {
	m := pyRouteDecoRe.FindStringSubmatch(deco)
	if m == nil {
		return
	}
	recv, kind, args := m[1], m[2], m[3]
	r, known := routers[recv]
	if !known {
		switch {
		case fileFW != "":
			r.fw = fileFW
		case recv == "app" || recv == "router" || recv == "api" || recv == "bp" || recv == "blueprint":
			r.fw = "fastapi"
			if kind == "route" {
				r.fw = "flask"
			}
		default:
			return
		}
	}
	parts := splitTop(args, ',')
	p, ok := "", false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if k, v, isKw := strings.Cut(part, "="); isKw && !strings.ContainsAny(k, `"'`) {
			k = strings.TrimSpace(k)
			if k == "path" || k == "rule" {
				p, ok = strLit(strings.TrimLeft(strings.TrimSpace(v), "rfu"))
			}
			continue
		}
		if !ok {
			p, ok = strLit(strings.TrimLeft(part, "rfu"))
		}
		break
	}
	if !ok || !(strings.HasPrefix(p, "/") || p == "" && r.prefix != "" || p == "" && known) {
		return
	}
	var methods []string
	summary := ""
	switch kind {
	case "route", "api_route":
		if mm := pyMethodsRe.FindStringSubmatch(args); mm != nil {
			for _, q := range pyQuotedRe.FindAllStringSubmatch(mm[1], -1) {
				methods = append(methods, strings.ToUpper(q[1]))
			}
		}
		if len(methods) == 0 {
			methods = []string{"GET"}
		}
	case "websocket":
		methods, summary = []string{"GET"}, "WebSocket"
	default:
		methods = []string{strings.ToUpper(kind)}
	}
	for _, mm := range methods {
		f.endpoints = append(f.endpoints, epRaw{node: "py:" + f.rel + ":" + recv, Endpoint: Endpoint{
			Method: mm, Path: joinPath(r.prefix, p), Handler: handler, File: f.rel, Line: line, Framework: r.fw,
			Source: "code", Summary: summary}})
	}
}

// pyMount records include_router / register_blueprint / mount.
func (a *analyzer) pyMount(f *fileInfo, stmt string, line int, routers map[string]pyRouter) {
	var parent, target, prefix string
	if m := pyIncludeRe.FindStringSubmatch(stmt); m != nil {
		parent, target = m[1], m[3]
		if pm := pyPrefixRe.FindStringSubmatch(m[4]); pm != nil {
			prefix = pm[1]
		}
	} else if m := pyMountRe.FindStringSubmatch(stmt); m != nil {
		parent, prefix, target = m[1], m[2], m[3]
	} else {
		return
	}
	mr := mountRaw{parent: "py:" + f.rel + ":" + parent, prefix: prefix, line: line}
	bind, attr, _ := strings.Cut(target, ".")
	if _, local := routers[bind]; local && attr == "" {
		mr.child = "py:" + f.rel + ":" + bind
	} else if _, ok := f.bindings[bind]; ok {
		mr.bind, mr.attr = bind, attr
	} else {
		return
	}
	f.mounts = append(f.mounts, mr)
}

// djangoRoutes reads path()/re_path() entries of a urls.py.
func (a *analyzer) djangoRoutes(f *fileInfo, clean string) {
	li := newLineIndex(clean)
	for _, m := range pyDjangoRe.FindAllStringSubmatchIndex(clean, -1) {
		if m[0] > 0 && (clean[m[0]-1] == '.' || isIdentByte(clean[m[0]-1])) {
			continue
		}
		open := m[1] - 1
		end := matchParen(clean, open, 5000)
		if end < 0 {
			continue
		}
		args := splitTop(clean[open+1:end-1], ',')
		if len(args) < 2 {
			continue
		}
		p, ok := strLit(strings.TrimLeft(strings.TrimSpace(args[0]), "rRuU"))
		if !ok {
			continue
		}
		if clean[m[2]:m[3]] != "path" {
			p = pyNamedGrpRe.ReplaceAllString(p, "{$1}")
			p = strings.TrimSuffix(strings.TrimPrefix(p, "^"), "$")
			p = strings.ReplaceAll(p, `\.`, ".")
		}
		view := strings.TrimSpace(args[1])
		if strings.HasPrefix(view, "include(") {
			inner := strings.TrimSuffix(strings.TrimPrefix(view, "include("), ")")
			if mod, ok := strLit(strings.TrimSpace(splitTop(inner, ',')[0])); ok {
				f.mounts = append(f.mounts, mountRaw{parent: "py:" + f.rel + ":", prefix: p, line: li.line(m[0]), module: mod})
			} else if tup := strings.TrimSpace(inner); strings.HasPrefix(tup, "(") {
				if mod, ok := strLit(strings.TrimSpace(splitTop(strings.Trim(tup, "()"), ',')[0])); ok {
					f.mounts = append(f.mounts, mountRaw{parent: "py:" + f.rel + ":", prefix: p, line: li.line(m[0]), module: mod})
				}
			}
			continue
		}
		f.endpoints = append(f.endpoints, epRaw{node: "py:" + f.rel + ":", Endpoint: Endpoint{Method: "ANY", Path: p,
			Handler: clip(view, 120), File: f.rel, Line: li.line(m[0]), Framework: "django", Source: "code"}})
	}
}

// pyMountChild resolves a Python mount's child node while linking.
func (a *analyzer) pyMountChild(f *fileInfo, m mountRaw) string {
	if m.module != "" {
		if t := a.pyResolve(f, 0, m.module); t != "" {
			return "py:" + t + ":"
		}
		return ""
	}
	b, ok := f.bindings[m.bind]
	if !ok || b.imp >= len(f.imports) {
		return ""
	}
	im := f.imports[b.imp]
	if b.name != "" {
		sub := b.name
		if im.spec != "" {
			sub = im.spec + "." + b.name
		}
		if t := a.pyResolve(f, im.level, sub); t != "" {
			return "py:" + t + ":" + m.attr
		}
		if im.target != "" {
			v := b.name
			if m.attr != "" {
				v = m.attr
			}
			return "py:" + im.target + ":" + v
		}
		return ""
	}
	if im.target != "" {
		return "py:" + im.target + ":" + m.attr
	}
	return ""
}
