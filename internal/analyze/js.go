package analyze

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// tsConfig is the alias part of a tsconfig.json / jsconfig.json.
type tsConfig struct {
	baseDir string   // resolved baseUrl ("" = root); valid when hasBase
	hasBase bool     // baseUrl was set (bare imports may resolve against it)
	paths   []tsPath // in declaration order
}

type tsPath struct {
	pattern string
	targets []string // slash paths relative to the root, may contain one *
}

// tsResolver resolves JS/TS import specifiers to files.
type tsResolver struct {
	configs   map[string]*tsConfig // dir → config found in that dir
	pkgByName map[string]string    // workspace package name → dir
	pkgMain   map[string][]string  // dir → main/module/types entries
	pkgDirs   map[string]bool      // dirs with a package.json
}

var jsExts = []string{".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".vue", ".svelte", ".json"}

var nodeBuiltins = map[string]bool{
	"assert": true, "async_hooks": true, "buffer": true, "child_process": true, "cluster": true, "console": true,
	"constants": true, "crypto": true, "dgram": true, "diagnostics_channel": true, "dns": true, "domain": true,
	"events": true, "fs": true, "http": true, "http2": true, "https": true, "inspector": true, "module": true,
	"net": true, "os": true, "path": true, "perf_hooks": true, "process": true, "punycode": true,
	"querystring": true, "readline": true, "repl": true, "stream": true, "string_decoder": true, "sys": true,
	"timers": true, "tls": true, "trace_events": true, "tty": true, "url": true, "util": true, "v8": true,
	"vm": true, "wasi": true, "worker_threads": true, "zlib": true, "test": true,
}

// prepareJS reads tsconfig/jsconfig aliases and workspace package.json files.
func (a *analyzer) prepareJS() {
	r := &tsResolver{configs: map[string]*tsConfig{}, pkgByName: map[string]string{}, pkgMain: map[string][]string{}, pkgDirs: map[string]bool{}}
	a.ts = r
	byDir := map[string][]*fileInfo{}
	for _, f := range a.files {
		if f.content == nil {
			continue
		}
		if f.base == "package.json" {
			r.pkgDirs[f.dir] = true
			var pj struct {
				Name    string `json:"name"`
				Main    string `json:"main"`
				Module  string `json:"module"`
				Types   string `json:"types"`
				Typings string `json:"typings"`
				Source  string `json:"source"`
			}
			if json.Unmarshal(f.content, &pj) == nil {
				if pj.Name != "" {
					if _, dup := r.pkgByName[pj.Name]; !dup {
						r.pkgByName[pj.Name] = f.dir
					}
				}
				for _, m := range []string{pj.Source, pj.Types, pj.Typings, pj.Module, pj.Main} {
					if m != "" {
						r.pkgMain[f.dir] = append(r.pkgMain[f.dir], path.Join(f.dir, m))
					}
				}
			}
		}
		if (strings.HasPrefix(f.base, "tsconfig") || strings.HasPrefix(f.base, "jsconfig")) && strings.HasSuffix(f.base, ".json") {
			byDir[f.dir] = append(byDir[f.dir], f)
		}
	}
	for dir, fs := range byDir {
		// tsconfig.json first, then the others (tsconfig.app.json…).
		sort.Slice(fs, func(i, j int) bool {
			ri, rj := fs[i].base == "tsconfig.json" || fs[i].base == "jsconfig.json", fs[j].base == "tsconfig.json" || fs[j].base == "jsconfig.json"
			if ri != rj {
				return ri
			}
			return fs[i].base < fs[j].base
		})
		merged := &tsConfig{}
		seen := map[string]bool{}
		for _, f := range fs {
			c := a.loadTSConfig(f.rel, 0)
			if c == nil {
				continue
			}
			if c.hasBase && !merged.hasBase {
				merged.hasBase, merged.baseDir = true, c.baseDir
			}
			for _, p := range c.paths {
				if !seen[p.pattern] {
					seen[p.pattern] = true
					merged.paths = append(merged.paths, p)
				}
			}
		}
		if merged.hasBase || len(merged.paths) > 0 {
			r.configs[dir] = merged
		}
	}
}

var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)

// loadTSConfig parses one tsconfig (comments and trailing commas allowed),
// following a relative "extends" up to three levels.
func (a *analyzer) loadTSConfig(rel string, depth int) *tsConfig {
	f := a.byPath[rel]
	if f == nil || f.content == nil || depth > 3 {
		return nil
	}
	txt := cleanCode(string(f.content), cleanOpts{slash: true, block: true})
	txt = trailingCommaRe.ReplaceAllString(txt, "$1")
	var raw struct {
		Extends         json.RawMessage `json:"extends"`
		CompilerOptions struct {
			BaseURL *string             `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal([]byte(txt), &raw) != nil {
		return nil
	}
	c := &tsConfig{}
	var exts []string
	var one string
	if json.Unmarshal(raw.Extends, &one) == nil && one != "" {
		exts = []string{one}
	} else {
		_ = json.Unmarshal(raw.Extends, &exts)
	}
	for _, e := range exts {
		if !strings.HasPrefix(e, ".") {
			continue
		}
		p := path.Join(f.dir, e)
		if !strings.HasSuffix(p, ".json") {
			p += ".json"
		}
		if parent := a.loadTSConfig(p, depth+1); parent != nil {
			*c = *parent
		}
	}
	base := f.dir
	if raw.CompilerOptions.BaseURL != nil {
		base = cleanRel(path.Join(f.dir, *raw.CompilerOptions.BaseURL))
		c.hasBase, c.baseDir = true, base
	} else if c.hasBase {
		base = c.baseDir
	}
	if raw.CompilerOptions.Paths != nil {
		c.paths = nil
		keys := sortedKeys(raw.CompilerOptions.Paths)
		for _, k := range keys {
			tp := tsPath{pattern: k}
			for _, t := range raw.CompilerOptions.Paths[k] {
				tp.targets = append(tp.targets, cleanRel(path.Join(base, t)))
			}
			c.paths = append(c.paths, tp)
		}
		// Longer (more specific) patterns first.
		sort.SliceStable(c.paths, func(i, j int) bool { return len(c.paths[i].pattern) > len(c.paths[j].pattern) })
	}
	return c
}

func cleanRel(p string) string {
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

// jsResolveFile tries p as a file, with extensions, and as a folder index.
func (a *analyzer) jsResolveFile(p string) string {
	p = cleanRel(p)
	if strings.HasPrefix(p, "..") {
		return ""
	}
	if f := a.byPath[p]; f != nil && path.Ext(p) != "" {
		return p
	}
	for _, e := range jsExts {
		if a.byPath[p+e] != nil {
			return p + e
		}
	}
	// ESM TypeScript writes "./x.js" for "./x.ts".
	for js, tss := range map[string][]string{".js": {".ts", ".tsx"}, ".jsx": {".tsx"}, ".mjs": {".mts"}, ".cjs": {".cts"}} {
		if strings.HasSuffix(p, js) {
			stem := strings.TrimSuffix(p, js)
			for _, t := range tss {
				if a.byPath[stem+t] != nil {
					return stem + t
				}
			}
		}
	}
	for _, m := range a.ts.pkgMain[p] {
		if r := a.jsResolveFileNoPkg(m); r != "" {
			return r
		}
	}
	return a.jsResolveFileNoPkg(p + "/index")
}

func (a *analyzer) jsResolveFileNoPkg(p string) string {
	p = cleanRel(p)
	if a.byPath[p] != nil && path.Ext(p) != "" {
		return p
	}
	for _, e := range jsExts {
		if a.byPath[p+e] != nil {
			return p + e
		}
	}
	if strings.HasSuffix(p, ".js") {
		stem := strings.TrimSuffix(p, ".js")
		for _, t := range []string{".ts", ".tsx"} {
			if a.byPath[stem+t] != nil {
				return stem + t
			}
		}
	}
	if !strings.HasSuffix(p, "/index") {
		for _, e := range jsExts {
			if a.byPath[p+"/index"+e] != nil {
				return p + "/index" + e
			}
		}
	}
	return ""
}

// jsResolve resolves a specifier from file f. It returns the target file
// ("" when not inside the folder) and the external package name ("" when
// internal, a builtin or unknown).
func (a *analyzer) jsResolve(f *fileInfo, spec string) (target, ext string) {
	if spec == "" || strings.Contains(spec, "://") || strings.HasPrefix(spec, "data:") {
		return "", ""
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == ".." {
		return a.jsResolveFile(path.Join(f.dir, spec)), ""
	}
	if strings.HasPrefix(spec, "/") {
		return a.jsResolveFile(strings.TrimPrefix(spec, "/")), ""
	}
	// tsconfig paths of the nearest folder that has a config.
	if c := a.nearestTSConfig(f.dir); c != nil {
		for _, p := range c.paths {
			star := strings.IndexByte(p.pattern, '*')
			var rest string
			switch {
			case star < 0 && spec == p.pattern:
			case star >= 0 && strings.HasPrefix(spec, p.pattern[:star]) && strings.HasSuffix(spec, p.pattern[star+1:]) && len(spec) >= len(p.pattern)-1:
				rest = spec[star : len(spec)-(len(p.pattern)-star-1)]
			default:
				continue
			}
			for _, t := range p.targets {
				if r := a.jsResolveFile(strings.Replace(t, "*", rest, 1)); r != "" {
					return r, ""
				}
			}
			return "", ""
		}
		if c.hasBase {
			if r := a.jsResolveFile(path.Join(c.baseDir, spec)); r != "" {
				return r, ""
			}
		}
	}
	// Workspace packages of a monorepo.
	name := npmName(spec)
	if dir, ok := a.ts.pkgByName[name]; ok {
		sub := strings.TrimPrefix(spec, name)
		if sub == "" {
			if r := a.jsResolveFile(dir); r != "" {
				return r, ""
			}
			for _, c := range []string{"src/index", "index", "src/main", "lib/index"} {
				if r := a.jsResolveFileNoPkg(path.Join(dir, c)); r != "" {
					return r, ""
				}
			}
			return "", ""
		}
		for _, d := range []string{dir, path.Join(dir, "src")} {
			if r := a.jsResolveFile(path.Join(d, sub)); r != "" {
				return r, ""
			}
		}
		return "", ""
	}
	// Common bundler aliases without a tsconfig: @/x, ~/x, $lib/x.
	for _, al := range []struct{ prefix, dir string }{{"@/", "src"}, {"~/", "src"}, {"$lib/", "src/lib"}, {"~/", ""}, {"@/", ""}} {
		if strings.HasPrefix(spec, al.prefix) {
			for d := f.dir; ; d = dirOf(d) {
				if a.ts.pkgDirs[d] || d == "" {
					if r := a.jsResolveFile(path.Join(d, al.dir, spec[len(al.prefix):])); r != "" {
						return r, ""
					}
					break
				}
			}
		}
	}
	if strings.HasPrefix(spec, "node:") || strings.HasPrefix(spec, "bun:") || strings.HasPrefix(spec, "virtual:") ||
		strings.HasPrefix(spec, "~") || strings.HasPrefix(spec, "#") || strings.HasPrefix(spec, "$") ||
		strings.HasPrefix(spec, "@/") || strings.HasPrefix(spec, "@@/") || strings.HasPrefix(spec, "\\") {
		return "", ""
	}
	if nodeBuiltins[strings.SplitN(spec, "/", 2)[0]] {
		return "", ""
	}
	if !validNpmName(name) {
		return "", ""
	}
	return "", name
}

var npmNameRe = regexp.MustCompile(`^(@[a-z0-9][\w.\-~]*/)?[a-z0-9][\w.\-~]*$`)

func validNpmName(n string) bool { return npmNameRe.MatchString(strings.ToLower(n)) }

// npmName is the package part of a bare specifier.
func npmName(spec string) string {
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func (a *analyzer) nearestTSConfig(dir string) *tsConfig {
	for d := dir; ; d = dirOf(d) {
		if c, ok := a.ts.configs[d]; ok {
			return c
		}
		if d == "" {
			return nil
		}
	}
}

var (
	// The whole-text patterns below are anchored (^) and run only where a
	// literal needle occurs (see anchoredAll): unanchored scans are far too
	// slow on big files.
	jsImportFromRe   = regexp.MustCompile(`^import\s+(type\s+)?([\w$*{}\s,]+?)\s*from\s*['"]([^'"\n]+)['"]`)
	jsImportBareRe   = regexp.MustCompile(`^import\s*['"]([^'"\n]+)['"]`)
	jsExportFromRe   = regexp.MustCompile(`^export\s+(type\s+)?([\w$*{}\s,]+?)\s*from\s*['"]([^'"\n]+)['"]`)
	jsRequireRe      = regexp.MustCompile(`^require\s*\(\s*['"]([^'"\n]+)['"]\s*\)`)
	jsRequireBindRe  = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*|\{[^}]*\})\s*=\s*$`)
	jsDynImportRe    = regexp.MustCompile(`^import\s*\(\s*['"]([^'"\n]+)['"]\s*\)`)
	jsScriptRe       = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script>`)
	jsFuncRe         = regexp.MustCompile(`^\s*(export\s+)?(default\s+)?(declare\s+)?(async\s+)?function\b\s*\*?\s*([A-Za-z_$][\w$]*)`)
	jsClassRe        = regexp.MustCompile(`^\s*(export\s+)?(default\s+)?(declare\s+)?(abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	jsIfaceRe        = regexp.MustCompile(`^\s*(export\s+)?(declare\s+)?interface\s+([A-Za-z_$][\w$]*)`)
	jsTypeRe         = regexp.MustCompile(`^\s*(export\s+)?(declare\s+)?type\s+([A-Za-z_$][\w$]*)\s*(<.*>)?\s*=`)
	jsEnumRe         = regexp.MustCompile(`^\s*(export\s+)?(declare\s+)?(const\s+)?enum\s+([A-Za-z_$][\w$]*)`)
	jsConstRe        = regexp.MustCompile(`^\s*(export\s+)?(declare\s+)?(const|let|var)\s+([A-Za-z_$][\w$]*)\s*(:[^=]*)?=\s*(.*)$`)
	jsArrowRe        = regexp.MustCompile(`^(async\s*)?(\([^()]*\)|[A-Za-z_$][\w$]*)\s*(:[^=]*)?=>`)
	jsFnExprRe       = regexp.MustCompile(`^(async\s+)?function\b`)
	jsWrapRe         = regexp.MustCompile(`^(React\.)?(memo|forwardRef|observer)\s*[(<]`)
	jsMethodRe       = regexp.MustCompile(`^\s*((?:public|private|protected|static|async|readonly|override|abstract|get|set)\s+)*\*?\s*(#?[A-Za-z_$][\w$]*)\s*(<[^>]*>)?\s*\(`)
	jsPropArrowRe    = regexp.MustCompile(`^\s*((?:public|private|protected|static|readonly)\s+)*(#?[A-Za-z_$][\w$]*)\s*(:[^=]*)?=\s*(async\s*)?(\(|[A-Za-z_$][\w$]*\s*=>)`)
	jsModExportsRe   = regexp.MustCompile(`^module\.exports\s*=\s*(\{[^}]*\}|[A-Za-z_$][\w$]*)`)
	jsExportsDotRe   = regexp.MustCompile(`^exports\.([A-Za-z_$][\w$]*)\s*=`)
	jsExportListRe   = regexp.MustCompile(`^export\s*\{([^}]*)\}\s*(?:;|\n|$)`)
	jsExportDefRe    = regexp.MustCompile(`^export\s+default\s+([A-Za-z_$][\w$]*)\s*(?:;|\n|$)`)
	jsChainMethodRe  = regexp.MustCompile(`^\s*\.\s*(get|post|put|patch|delete|del|head|options|all)\s*\(`)
	jsMountTargetRe  = regexp.MustCompile(`^\s*,\s*([A-Za-z_$][\w$.]*)\s*(\(\s*\))?\s*[,)]`)
	jsRegisterRe     = regexp.MustCompile(`^\(\s*([A-Za-z_$][\w$.]*)\s*,\s*\{[^}]*\bprefix\s*:\s*['"]([^'"\n]+)['"]`)
	jsRouterVarRe    = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:new\s+)?(?:express\s*\.\s*)?(Router|Hono|Koa|Elysia|express|fastify|Fastify)\s*\(([^)]*)\)\s*(?:\.\s*basePath\s*\(\s*['"]([^'"]+)['"]\s*\))?`)
	jsObjMethodRe    = regexp.MustCompile(`\bmethod\s*:\s*(\[[^\]]*\]|['"][A-Za-z]+['"])`)
	jsObjURLRe       = regexp.MustCompile(`\b(?:url|path)\s*:\s*['"]([^'"\n]+)['"]`)
	jsObjHandlerRe   = regexp.MustCompile(`\bhandler\s*:\s*([A-Za-z_$][\w$.]*)`)
	jsQuotedRe       = regexp.MustCompile(`['"]([^'"]+)['"]`)
	jsNestCtrlRe     = regexp.MustCompile(`^\s*@Controller\s*\(\s*(?:\{[^}]*path\s*:\s*)?(?:['"]([^'"]*)['"])?`)
	jsNestRouteRe    = regexp.MustCompile(`^\s*@(Get|Post|Put|Patch|Delete|Head|Options|All)\s*\(\s*(?:['"]([^'"]*)['"])?`)
	jsNestHandlerRe  = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|async)\s+)*([A-Za-z_$][\w$]*)\s*\(`)
	jsNextMethodRe   = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?(?:function\s+|const\s+|let\s+)(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	jsNextReexportRe = regexp.MustCompile(`(?m)^\s*export\s*\{([^}]*)\}`)
	jsEnvDotRe       = regexp.MustCompile(`^\.([A-Za-z_][A-Za-z0-9_]*)(?:\s*(?:\|\||\?\?)\s*['"` + "`" + `]([^'"` + "`" + `\n]*)['"` + "`" + `])?`)
	jsEnvIdxRe       = regexp.MustCompile(`^\[\s*['"]([^'"\n]+)['"]\s*\](?:\s*(?:\|\||\?\?)\s*['"]([^'"\n]*)['"])?`)
	jsEnvDestructRe  = regexp.MustCompile(`\{([^{}]*)\}\s*=\s*$`)
	jsPrefixOptRe    = regexp.MustCompile(`prefix\s*:\s*['"]([^'"]+)['"]`)
	jsDenoEnvRe      = regexp.MustCompile(`^Deno\.env\.get\(\s*['"]([^'"\n]+)['"]`)
)

var jsKeywords = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true,
	"function": true, "constructor": true, "super": true, "new": true, "else": true, "do": true, "try": true,
	"typeof": true, "await": true, "yield": true, "throw": true, "delete": true, "void": true, "with": true}

var viteBuiltinEnv = map[string]bool{"MODE": true, "DEV": true, "PROD": true, "SSR": true, "BASE_URL": true}

// jsClientNames are receivers that make HTTP requests rather than serve them.
var jsClientNames = map[string]bool{"axios": true, "ky": true, "superagent": true, "got": true, "request": true,
	"http": true, "https": true, "fetch": true, "$http": true, "client": true, "apiClient": true, "instance": true,
	"cache": true, "map": true, "params": true, "searchParams": true, "headers": true, "cookies": true,
	"localStorage": true, "sessionStorage": true, "req": true, "res": true, "ctx": true, "store": true,
	"redis": true, "db": true, "url": true, "formData": true, "supertest": true, "agent": true, "this": true,
	"window": true, "document": true, "Reflect": true, "Object": true, "Array": true, "c": true, "context": true}

var jsServerPkgs = map[string]string{"express": "express", "koa": "koa", "koa-router": "koa", "@koa/router": "koa",
	"fastify": "fastify", "hono": "hono", "elysia": "elysia", "restify": "restify", "polka": "polka",
	"@hono/node-server": "hono", "router": "express"}

// scriptBlocks keeps only the <script> contents of a .vue/.svelte file,
// blanking the rest so line numbers stay.
func scriptBlocks(src string) string {
	b := []byte(src)
	for i := range b {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	for _, m := range jsScriptRe.FindAllStringSubmatchIndex(src, -1) {
		copy(b[m[2]:m[3]], src[m[2]:m[3]])
	}
	return string(b)
}

// parseJS reads one JS/TS (or Vue/Svelte) file with line-based patterns.
func (a *analyzer) parseJS(f *fileInfo) {
	src := string(f.content)
	if f.lang == "Vue" || f.lang == "Svelte" {
		src = scriptBlocks(src)
	}
	clean := cleanCode(src, jsClean)
	bare := cleanCode(src, jsBare)
	f.refText = bare
	li := newLineIndex(clean)
	rawLines := strings.Split(src, "\n")
	bareLines := strings.Split(bare, "\n")
	cleanLines := strings.Split(clean, "\n")
	depth := braceDepths(bareLines)
	f.bindings = map[string]binding{}

	// --- imports ---
	extSeen := map[string]bool{}
	addImport := func(spec string, names []string, off int) int {
		target, ext := a.jsResolve(f, spec)
		f.imports = append(f.imports, rawImport{spec: spec, names: names, line: li.line(off), target: target})
		if ext != "" && !extSeen[ext] {
			extSeen[ext] = true
			f.externals = append(f.externals, extRef{"npm", ext})
		}
		return len(f.imports) - 1
	}
	for _, kw := range []string{"import", "export"} {
		re := jsImportFromRe
		if kw == "export" {
			re = jsExportFromRe
		}
		for _, m := range anchoredAll(clean, kw, re, 4000) {
			clause, spec := clean[m[4]:m[5]], clean[m[6]:m[7]]
			names, locals := jsClauseNames(clause)
			idx := addImport(spec, names, m[0])
			if kw == "import" {
				for local, imported := range locals {
					f.bindings[local] = binding{imp: idx, name: imported}
				}
			}
		}
	}
	for _, m := range anchoredAll(clean, "import", jsImportBareRe, 600) {
		addImport(clean[m[2]:m[3]], nil, m[0])
	}
	for _, m := range anchoredAll(clean, "require", jsRequireRe, 600) {
		spec := clean[m[2]:m[3]]
		var names []string
		bind := ""
		if bm := jsRequireBindRe.FindStringSubmatch(clean[max(0, m[0]-300):m[0]]); bm != nil {
			bind = bm[1]
			if strings.HasPrefix(bind, "{") {
				ns, _ := jsClauseNames(strings.ReplaceAll(bind, ":", " as "))
				names, bind = ns, ""
			}
		}
		idx := addImport(spec, names, m[0])
		if bind != "" {
			f.bindings[bind] = binding{imp: idx}
		}
	}
	for _, m := range anchoredAll(clean, "import", jsDynImportRe, 600) {
		addImport(clean[m[2]:m[3]], nil, m[0])
	}

	// --- symbols ---
	isJSX := strings.HasSuffix(f.base, ".tsx") || strings.HasSuffix(f.base, ".jsx") ||
		(f.lang == "JavaScript" && strings.Contains(clean, "from 'react'") || strings.Contains(clean, `from "react"`))
	exported := map[string]bool{}
	for _, m := range anchoredAll(clean, "module.exports", jsModExportsRe, 2000) {
		v := clean[m[2]:m[3]]
		if strings.HasPrefix(v, "{") {
			for _, part := range strings.Split(strings.Trim(v, "{}"), ",") {
				k, _, _ := strings.Cut(strings.TrimSpace(part), ":")
				exported[strings.TrimSpace(k)] = true
			}
		} else {
			exported[v] = true
		}
	}
	for _, m := range anchoredAll(clean, "exports", jsExportsDotRe, 200) {
		exported[clean[m[2]:m[3]]] = true
	}
	for _, m := range anchoredAll(clean, "export", jsExportListRe, 2000) {
		for _, part := range strings.Split(clean[m[2]:m[3]], ",") {
			fs := strings.Fields(part)
			if len(fs) > 0 && fs[0] != "type" {
				exported[fs[0]] = true
			} else if len(fs) > 1 {
				exported[fs[1]] = true
			}
		}
	}
	for _, m := range anchoredAll(clean, "export", jsExportDefRe, 200) {
		exported[clean[m[2]:m[3]]] = true
	}
	type classRange struct {
		name     string
		start    int
		end      int
		exported bool
	}
	var classes []classRange
	add := func(i int, name, kind string, exp bool) int {
		sig := jsSignature(cleanLines[i])
		s := Symbol{File: f.rel, Name: name, Kind: kind, Line: i + 1, Exported: exp || exported[name], Signature: sig,
			Doc: docAbove(rawLines, i, false)}
		end := blockEnd(bareLines, depth, i)
		if end > i {
			s.EndLine = end + 1
		}
		f.syms = append(f.syms, s)
		return end
	}
	for i, l := range bareLines {
		if depth[i] != 0 || len(l) > 2000 || strings.TrimSpace(l) == "" {
			continue
		}
		if m := jsFuncRe.FindStringSubmatch(l); m != nil {
			kind := "func"
			if isJSX && isCapital(m[5]) {
				kind = "component"
			}
			add(i, m[5], kind, m[1] != "")
			continue
		}
		if m := jsClassRe.FindStringSubmatch(l); m != nil {
			end := add(i, m[5], "class", m[1] != "")
			classes = append(classes, classRange{m[5], i, end, m[1] != "" || exported[m[5]]})
			continue
		}
		if m := jsIfaceRe.FindStringSubmatch(l); m != nil {
			add(i, m[3], "interface", m[1] != "")
			continue
		}
		if m := jsTypeRe.FindStringSubmatch(l); m != nil {
			add(i, m[3], "type", m[1] != "")
			continue
		}
		if m := jsEnumRe.FindStringSubmatch(l); m != nil {
			add(i, m[4], "enum", m[1] != "")
			continue
		}
		if m := jsConstRe.FindStringSubmatch(cleanLines[i]); m != nil && strings.TrimSpace(bareLines[i]) != "" {
			name, rest := m[4], strings.TrimSpace(m[6])
			isFn := jsArrowRe.MatchString(rest) || jsFnExprRe.MatchString(rest)
			if !isFn && (strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "async")) {
				// Multi-line arrow parameters.
				for k := i; k < len(bareLines) && k < i+6; k++ {
					if strings.Contains(bareLines[k], "=>") {
						isFn = true
						break
					}
					if strings.Contains(bareLines[k], ";") {
						break
					}
				}
			}
			wrapped := jsWrapRe.MatchString(rest)
			exp := m[1] != "" || exported[name]
			switch {
			case (isFn || wrapped) && isJSX && isCapital(name):
				add(i, name, "component", exp)
			case isFn:
				add(i, name, "func", exp)
			case exp && m[3] == "const":
				add(i, name, "const", exp)
			}
		}
	}
	for _, c := range classes {
		cd := depth[c.start]
		for i := c.start + 1; i <= c.end && i < len(bareLines); i++ {
			if depth[i] != cd+1 {
				continue
			}
			l := bareLines[i]
			var name string
			if m := jsMethodRe.FindStringSubmatch(l); m != nil && strings.ContainsAny(l, "{") || m != nil && i+1 < len(bareLines) && strings.HasPrefix(strings.TrimSpace(bareLines[i+1]), "{") {
				name = m[2]
			} else if m := jsPropArrowRe.FindStringSubmatch(l); m != nil && strings.Contains(l, "=>") {
				name = m[2]
			}
			if name == "" || jsKeywords[name] {
				continue
			}
			private := strings.HasPrefix(name, "#") || strings.Contains(l, "private ")
			s := Symbol{File: f.rel, Name: strings.TrimPrefix(name, "#"), Kind: "method", Parent: c.name, Line: i + 1,
				Exported: c.exported && !private, Signature: jsSignature(cleanLines[i]), Doc: docAbove(rawLines, i, false)}
			if end := blockEnd(bareLines, depth, i); end > i {
				s.EndLine = end + 1
			}
			f.syms = append(f.syms, s)
		}
	}
	if f.lang == "Vue" || f.lang == "Svelte" {
		name := strings.TrimSuffix(f.base, path.Ext(f.base))
		f.syms = append(f.syms, Symbol{File: f.rel, Name: name, Kind: "component", Line: 1, EndLine: f.lines, Exported: true})
	}

	// --- routes ---
	a.jsRoutes(f, clean, cleanLines, li)

	// --- env ---
	for _, src := range []string{"process.env", "import.meta.env", "Bun.env"} {
		for _, p := range needleAll(clean, src) {
			at := p + len(src)
			rest := clean[at:min(len(clean), at+400)]
			if m := jsEnvDotRe.FindStringSubmatch(rest); m != nil {
				if src == "import.meta.env" && viteBuiltinEnv[m[1]] {
					continue
				}
				f.envs = append(f.envs, envUse{name: m[1], def: m[2], line: li.line(p)})
				continue
			}
			if m := jsEnvIdxRe.FindStringSubmatch(rest); m != nil {
				f.envs = append(f.envs, envUse{name: m[1], def: m[2], line: li.line(p)})
				continue
			}
			if src != "process.env" {
				continue
			}
			if m := jsEnvDestructRe.FindStringSubmatch(clean[max(0, p-1000):p]); m != nil {
				for _, part := range strings.Split(m[1], ",") {
					k, def, hasDef := strings.Cut(part, "=")
					k, _, _ = strings.Cut(k, ":")
					k = strings.TrimSpace(k)
					if looseEnvRe.MatchString(k) {
						e := envUse{name: k, line: li.line(p)}
						if hasDef {
							if s, ok := strLit(def); ok {
								e.def = s
							}
						}
						f.envs = append(f.envs, e)
					}
				}
			}
		}
	}
	for _, m := range anchoredAll(clean, "Deno.env.get", jsDenoEnvRe, 300) {
		f.envs = append(f.envs, envUse{name: clean[m[2]:m[3]], line: li.line(m[0])})
	}
}

func isCapital(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

// jsClauseNames reads an import clause: the imported names (for the edge)
// and local name → imported name ("" = default or namespace).
func jsClauseNames(clause string) ([]string, map[string]string) {
	var names []string
	locals := map[string]string{}
	clause = strings.TrimSpace(clause)
	outside := clause
	if i := strings.IndexByte(clause, '{'); i >= 0 {
		j := strings.LastIndexByte(clause, '}')
		if j > i {
			for _, part := range strings.Split(clause[i+1:j], ",") {
				fs := strings.Fields(part)
				if len(fs) > 0 && fs[0] == "type" {
					fs = fs[1:]
				}
				if len(fs) == 0 {
					continue
				}
				imported, local := fs[0], fs[0]
				if len(fs) >= 3 && fs[1] == "as" {
					local = fs[2]
				}
				names = append(names, imported)
				locals[local] = imported
			}
			outside = clause[:i] + clause[j+1:]
		}
	}
	for _, part := range strings.Split(outside, ",") {
		fs := strings.Fields(part)
		switch {
		case len(fs) == 0:
		case fs[0] == "*" && len(fs) >= 3:
			locals[fs[2]] = ""
		case len(fs) == 1 && fs[0] != "type":
			locals[fs[0]] = ""
			names = append(names, "default")
		}
	}
	return uniqSorted(names), locals
}

// jsSignature is the declaration line without its body.
func jsSignature(l string) string {
	s := strings.TrimSpace(l)
	if i := strings.Index(s, "=>"); i >= 0 {
		s = s[:i+2]
	} else if strings.HasSuffix(s, "{") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "{"))
	}
	return clip(s, 160)
}

// jsRoutes finds server routes in one file.
func (a *analyzer) jsRoutes(f *fileInfo, clean string, lines []string, li lineIndex) {
	// File-system routers first (Next.js, SvelteKit).
	a.jsFileRoutes(f, clean, li)

	fw := ""
	frontend := strings.HasSuffix(f.base, ".tsx") || strings.HasSuffix(f.base, ".jsx") || f.lang == "Vue" || f.lang == "Svelte"
	nest := false
	for _, im := range f.imports {
		if w, ok := jsServerPkgs[im.spec]; ok && fw == "" {
			fw = w
		}
		if strings.HasPrefix(im.spec, "hono") && fw == "" {
			fw = "hono"
		}
		switch {
		case im.spec == "@nestjs/common":
			nest = true
		case im.spec == "react" || im.spec == "vue" || im.spec == "axios" || strings.HasPrefix(im.spec, "@angular/") || im.spec == "svelte":
			frontend = true
		}
	}
	if nest {
		a.nestRoutes(f, lines)
	}
	if frontend && fw == "" {
		return
	}

	// Router variables with their own prefix (koa-router prefix, hono basePath).
	varPrefix := map[string]string{}
	localRouter := map[string]bool{}
	for _, kw := range []string{"Router(", "Hono(", "Koa(", "Elysia(", "express(", "fastify(", "Fastify("} {
		if !strings.Contains(clean, kw) {
			continue
		}
		for _, l := range lines {
			if !strings.Contains(l, kw) {
				continue
			}
			for _, m := range jsRouterVarRe.FindAllStringSubmatch(l, -1) {
				localRouter[m[1]] = true
				if m[4] != "" {
					varPrefix[m[1]] = m[4]
				} else if pm := jsPrefixOptRe.FindStringSubmatch(m[3]); pm != nil {
					varPrefix[m[1]] = pm[1]
				}
			}
		}
	}
	accept := func(recv string) (string, bool) {
		if jsClientNames[recv] || recv == "" {
			return "", false
		}
		if fw != "" {
			return fw, true
		}
		switch recv {
		case "fastify":
			return "fastify", true
		case "app", "router", "server", "api", "routes":
			return "express", true
		}
		return "", false
	}
	type jsEP struct {
		ep   Endpoint
		recv string
	}
	var eps []jsEP
	addEP := func(recv, method, p, handler string, off int, w string) {
		eps = append(eps, jsEP{recv: recv, ep: Endpoint{Method: method, Path: joinPath(varPrefix[recv], p), Handler: handler,
			File: f.rel, Line: li.line(off), Framework: w, Source: "code"}})
	}
	prefixOf := map[string][]string{}
	for _, c := range jsMemberCalls(clean, jsRouteNames) {
		w, ok := accept(c.recv)
		if !ok {
			continue
		}
		p, after, isStr := jsStringArg(clean, c.open)
		next := skipSpace(clean, after)
		switch c.name {
		case "route":
			if !isStr {
				// fastify.route({ method, url, handler })
				o := skipSpace(clean, c.open+1)
				if o >= len(clean) || clean[o] != '{' {
					continue
				}
				end := matchParen(clean, o, 4000)
				if end < 0 {
					continue
				}
				obj := clean[o:end]
				um := jsObjURLRe.FindStringSubmatch(obj)
				mm := jsObjMethodRe.FindStringSubmatch(obj)
				if um == nil || mm == nil {
					continue
				}
				handler := ""
				if hm := jsObjHandlerRe.FindStringSubmatch(obj); hm != nil {
					handler = hm[1]
				}
				for _, q := range jsQuotedRe.FindAllStringSubmatch(mm[1], -1) {
					addEP(c.recv, strings.ToUpper(q[1]), um[1], handler, c.pos, w)
				}
				continue
			}
			if next < len(clean) && clean[next] == ')' && strings.HasPrefix(p, "/") {
				// express: router.route('/x').get(h).post(h2)
				pos := next + 1
				for k := 0; k < 12; k++ {
					cm := jsChainMethodRe.FindStringSubmatchIndex(clean[pos:min(len(clean), pos+400)])
					if cm == nil {
						break
					}
					open := pos + cm[1] - 1
					end := matchParen(clean, open, 20000)
					if end < 0 {
						break
					}
					addEP(c.recv, jsMethod(clean[pos+cm[2]:pos+cm[3]]), p, jsHandlerText(clean, open), pos+cm[2], w)
					pos = end
				}
				continue
			}
			fallthrough
		case "use":
			// app.use('/api', router) / hono app.route('/api', sub)
			if !isStr || !strings.HasPrefix(p, "/") {
				continue
			}
			m := jsMountTargetRe.FindStringSubmatch(clean[after:min(len(clean), after+300)])
			if m == nil {
				continue
			}
			bind := strings.SplitN(m[1], ".", 2)[0]
			if localRouter[bind] && bind != c.recv {
				prefixOf[bind] = append(prefixOf[bind], joinPath(varPrefix[c.recv], p))
				continue
			}
			if _, ok := f.bindings[bind]; ok {
				f.mounts = append(f.mounts, mountRaw{parent: "js:" + f.rel, prefix: joinPath(varPrefix[c.recv], p), line: li.line(c.pos), bind: bind})
			}
		case "register":
			m := jsRegisterRe.FindStringSubmatch(clean[c.open:min(len(clean), c.open+600)])
			if m == nil {
				continue
			}
			bind := strings.SplitN(m[1], ".", 2)[0]
			if _, ok := f.bindings[bind]; ok {
				f.mounts = append(f.mounts, mountRaw{parent: "js:" + f.rel, prefix: m[2], line: li.line(c.pos), bind: bind})
			}
		default:
			if !isStr || next >= len(clean) || clean[next] != ',' || !(strings.HasPrefix(p, "/") || p == "*") {
				continue
			}
			addEP(c.recv, jsMethod(c.name), p, jsHandlerText(clean, c.open), c.pos, w)
		}
	}
	for _, e := range eps {
		if ps, ok := prefixOf[e.recv]; ok {
			for _, p := range ps {
				ep := e.ep
				ep.Path = joinPath(p, ep.Path)
				f.endpoints = append(f.endpoints, epRaw{Endpoint: ep, node: "js:" + f.rel})
			}
			continue
		}
		f.endpoints = append(f.endpoints, epRaw{Endpoint: e.ep, node: "js:" + f.rel})
	}
}

var jsRouteNames = toSet("get post put patch delete del head options all any route use register")

// jsCall is a member call "recv.name(" found by jsMemberCalls.
type jsCall struct {
	recv string
	name string
	pos  int // the dot
	open int // the (
}

// jsMemberCalls finds recv.name( calls for the given method names with a
// hand-written scan (much faster than a regular expression on big files).
func jsMemberCalls(s string, names map[string]bool) []jsCall {
	var out []jsCall
	for i := 0; i < len(s); i++ {
		if s[i] != '.' {
			continue
		}
		j := skipSpace(s, i+1)
		k := j
		for k < len(s) && isIdentByte(s[k]) {
			k++
		}
		if k == j || !names[s[j:k]] {
			continue
		}
		o := skipSpace(s, k)
		if o >= len(s) || s[o] != '(' {
			continue
		}
		b := i - 1
		for b >= 0 && (s[b] == ' ' || s[b] == '\t' || s[b] == '\n' || s[b] == '\r') {
			b--
		}
		e := b + 1
		for b >= 0 && isIdentByte(s[b]) {
			b--
		}
		out = append(out, jsCall{recv: s[b+1 : e], name: s[j:k], pos: i, open: o})
		i = o
	}
	return out
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// jsStringArg reads a string literal right after the ( at open.
func jsStringArg(s string, open int) (val string, after int, ok bool) {
	i := skipSpace(s, open+1)
	if i >= len(s) || (s[i] != '\'' && s[i] != '"' && s[i] != '`') {
		return "", open + 1, false
	}
	q := s[i]
	j := i + 1
	for j < len(s) && s[j] != q && s[j] != '\n' {
		if s[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(s) || s[j] != q {
		return "", open + 1, false
	}
	return s[i+1 : j], j + 1, true
}

func jsMethod(m string) string {
	switch m {
	case "del":
		return "DELETE"
	case "all", "any":
		return "ANY"
	}
	return strings.ToUpper(m)
}

// jsHandlerText describes the last argument of the call opened at s[open].
func jsHandlerText(s string, open int) string {
	end := matchParen(s, open, 200000)
	if end < 0 {
		return "inline handler"
	}
	args := splitTop(s[open+1:end-1], ',')
	last := strings.TrimSpace(args[len(args)-1])
	if strings.Contains(last, "=>") || strings.HasPrefix(last, "function") || strings.HasPrefix(last, "async") {
		return "inline handler"
	}
	return clip(last, 120)
}

// nestRoutes reads NestJS @Controller / @Get decorators.
func (a *analyzer) nestRoutes(f *fileInfo, lines []string) {
	prefix := ""
	for i, l := range lines {
		if m := jsNestCtrlRe.FindStringSubmatch(l); m != nil {
			prefix = m[1]
			continue
		}
		m := jsNestRouteRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		handler := ""
		for k := i + 1; k < len(lines) && k < i+8; k++ {
			t := strings.TrimSpace(lines[k])
			if strings.HasPrefix(t, "@") || t == "" {
				continue
			}
			if hm := jsNestHandlerRe.FindStringSubmatch(lines[k]); hm != nil {
				handler = hm[1]
			}
			break
		}
		f.endpoints = append(f.endpoints, epRaw{node: "js:" + f.rel, Endpoint: Endpoint{Method: jsMethod(strings.ToLower(m[1])),
			Path: joinPath("/"+strings.Trim(prefix, "/"), m[2]), Handler: handler, File: f.rel, Line: i + 1, Framework: "nestjs", Source: "code"}})
	}
}

// jsFileRoutes derives routes from Next.js / SvelteKit file names.
func (a *analyzer) jsFileRoutes(f *fileInfo, clean string, li lineIndex) {
	segs := strings.Split(f.rel, "/")
	stem := strings.TrimSuffix(f.base, path.Ext(f.base))
	rootAt := func(name string) int {
		for i, s := range segs[:len(segs)-1] {
			if s == name && (i == 0 || segs[i-1] == "src" || a.ts.pkgDirs[strings.Join(segs[:i], "/")]) {
				return i
			}
		}
		return -1
	}
	exportsMethods := func() []struct {
		m    string
		line int
	} {
		var out []struct {
			m    string
			line int
		}
		for _, m := range jsNextMethodRe.FindAllStringSubmatchIndex(clean, -1) {
			out = append(out, struct {
				m    string
				line int
			}{clean[m[2]:m[3]], li.line(m[2])})
		}
		for _, m := range jsNextReexportRe.FindAllStringSubmatchIndex(clean, -1) {
			for _, part := range strings.Split(clean[m[2]:m[3]], ",") {
				fs := strings.Fields(part)
				if n := fs; len(n) > 0 && httpMethods[n[len(n)-1]] {
					out = append(out, struct {
						m    string
						line int
					}{n[len(n)-1], li.line(m[0])})
				}
			}
		}
		return out
	}
	emit := func(segments []string, fw string, catchAll string) {
		p, ok := fsRoutePath(segments)
		if !ok {
			return
		}
		if catchAll != "" {
			p = joinPath(p, catchAll)
		}
		for _, m := range exportsMethods() {
			f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: m.m, Path: p, Handler: m.m, File: f.rel,
				Line: m.line, Framework: fw, Source: "code"}})
		}
	}
	switch {
	case stem == "route" && f.lang != "Vue":
		if i := rootAt("app"); i >= 0 {
			emit(segs[i+1:len(segs)-1], "nextjs", "")
		}
	case stem == "+server":
		for i := range segs[:len(segs)-1] {
			if segs[i] == "routes" && i > 0 && segs[i-1] == "src" {
				emit(segs[i+1:len(segs)-1], "sveltekit", "")
				break
			}
		}
	default:
		i := rootAt("pages")
		if i < 0 || i+1 >= len(segs)-1 || segs[i+1] != "api" || strings.HasPrefix(stem, "_") || strings.HasSuffix(f.base, ".d.ts") {
			return
		}
		rest := append([]string{}, segs[i+1:len(segs)-1]...)
		if stem != "index" {
			rest = append(rest, stem)
		}
		p, ok := fsRoutePath(rest)
		if !ok {
			return
		}
		line := 1
		if j := strings.Index(clean, "export default"); j >= 0 {
			line = li.line(j)
		}
		f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: "ANY", Path: p, Handler: "default export",
			File: f.rel, Line: line, Framework: "nextjs", Source: "code"}})
	}
}

// fsRoutePath turns folder names into a route: (group) and @slot dropped,
// [id] / [...slug] / [[...slug]] → {id} / {slug}; _private folders are not routes.
func fsRoutePath(segs []string) (string, bool) {
	var out []string
	for _, s := range segs {
		switch {
		case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"), strings.HasPrefix(s, "@"):
		case strings.HasPrefix(s, "_"):
			return "", false
		case strings.HasPrefix(s, "["):
			name := strings.Trim(s, "[]")
			name = strings.TrimPrefix(name, "...")
			if i := strings.IndexByte(name, '='); i >= 0 {
				name = name[:i]
			}
			out = append(out, "{"+name+"}")
		default:
			out = append(out, s)
		}
	}
	return "/" + strings.Join(out, "/"), true
}
