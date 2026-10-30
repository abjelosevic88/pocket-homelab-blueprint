package analyze

import (
	"path"
	"regexp"
	"strings"
)

// srcLines holds the three views of a C-like source file.
type srcLines struct {
	raw, clean, bare []string
	cleanText        string
	depth            []int
	li               lineIndex
}

func newSrcLines(src string, clean, bare cleanOpts) *srcLines {
	c := cleanCode(src, clean)
	b := cleanCode(src, bare)
	s := &srcLines{raw: strings.Split(src, "\n"), clean: strings.Split(c, "\n"), bare: strings.Split(b, "\n"), cleanText: c}
	s.depth = braceDepths(s.bare)
	s.li = newLineIndex(c)
	return s
}

// container is a class/impl/trait/module block.
type container struct {
	name     string
	start    int
	end      int
	depth    int
	exported bool
}

func popContainers(st []container, i int) []container {
	for len(st) > 0 && st[len(st)-1].end < i {
		st = st[:len(st)-1]
	}
	return st
}

func addEnvMatches(f *fileInfo, text string, li lineIndex, re *regexp.Regexp) {
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		e := envUse{name: text[m[2]:m[3]], line: li.line(m[0])}
		if len(m) > 4 && m[4] >= 0 {
			e.def = text[m[4]:m[5]]
		}
		if looseEnvRe.MatchString(e.name) {
			f.envs = append(f.envs, e)
		}
	}
}

// restRoutes expands a resource declaration (Rails resources, Laravel
// Route::resource) into its REST routes.
func restRoutes(f *fileInfo, base, ctrl string, singular, api bool, filter func(string) bool, fw string, line int) {
	type r struct{ action, method, suffix string }
	var rs []r
	if singular {
		rs = []r{{"new", "GET", "/new"}, {"create", "POST", ""}, {"show", "GET", ""}, {"edit", "GET", "/edit"}, {"update", "PATCH", ""}, {"destroy", "DELETE", ""}}
	} else {
		rs = []r{{"index", "GET", ""}, {"new", "GET", "/new"}, {"create", "POST", ""}, {"show", "GET", "/{id}"}, {"edit", "GET", "/{id}/edit"}, {"update", "PATCH", "/{id}"}, {"destroy", "DELETE", "/{id}"}}
	}
	for _, x := range rs {
		if api && (x.action == "new" || x.action == "edit") {
			continue
		}
		if filter != nil && !filter(x.action) {
			continue
		}
		h := ctrl + "#" + x.action
		if fw == "laravel" {
			h = ctrl + "@" + map[string]string{"new": "create", "create": "store"}[x.action]
			if strings.HasSuffix(h, "@") {
				h += x.action
			}
		}
		f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: x.method, Path: base + x.suffix, Handler: h,
			File: f.rel, Line: line, Framework: fw, Source: "code"}})
	}
}

// --- Rust ---

var (
	rsImplRe   = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl\b(?:\s*<[^{]*?>)?\s+(?:([\w:]+)(?:<[^{]*>)?\s+for\s+)?&?([\w:]+)`)
	rsTraitRe  = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_]\w*)`)
	rsModRe    = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_]\w*)`)
	rsFnRe     = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(?:default\s+)?(?:const\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+(?:"[^"]*"\s+)?)?fn\s+([A-Za-z_]\w*)`)
	rsItemRe   = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(struct|enum|union|type|const|static)\s+(?:mut\s+)?([A-Za-z_]\w*)`)
	rsUseRe    = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?use\s+(?:::)?([A-Za-z_]\w*)`)
	rsExternRe = regexp.MustCompile(`(?m)^\s*extern\s+crate\s+([A-Za-z_]\w*)`)
	rsPathRe   = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)::`)
	rsRouteRe  = regexp.MustCompile(`\.route\s*\(\s*"([^"]*)"\s*,`)
	rsAxumMRe  = regexp.MustCompile(`\b(get|post|put|patch|delete|head|options|any)\s*\(\s*([\w:]+)`)
	rsActixMRe = regexp.MustCompile(`web::(get|post|put|patch|delete|head)\s*\(\s*\)\s*\.to\s*\(\s*([\w:]+)`)
	rsResRe    = regexp.MustCompile(`web::resource\s*\(\s*"([^"]*)"\s*\)`)
	rsAttrRe   = regexp.MustCompile(`^\s*#\[(get|post|put|patch|delete|head|options)\s*\(\s*"([^"]*)"`)
	rsEnvRe    = regexp.MustCompile(`\b(?:env::var(?:_os)?|option_env!|env!|dotenvy::var|dotenv::var)\s*\(\s*"([^"]+)"\s*\)(?:\s*\.\s*unwrap_or(?:_else)?\s*\(\s*(?:\|[^|]*\|\s*)?"([^"]*)")?`)
	rsKeywords = map[string]bool{"crate": true, "self": true, "super": true, "std": true, "core": true, "alloc": true, "Self": true}
)

func parseRust(f *fileInfo) {
	s := newSrcLines(string(f.content), cClean, cBare)
	f.refText = strings.Join(s.bare, "\n")
	var st []container
	for i, l := range s.bare {
		st = popContainers(st, i)
		if strings.TrimSpace(l) == "" {
			continue
		}
		d := s.depth[i]
		inContainer := len(st) > 0 && d == st[len(st)-1].depth+1
		if d != 0 && !inContainer {
			continue
		}
		parent := ""
		parentKind := ""
		if inContainer {
			parent = st[len(st)-1].name
			parentKind = "x"
		}
		sym := func(name, kind string, exp bool) int {
			sy := Symbol{File: f.rel, Name: name, Kind: kind, Line: i + 1, Exported: exp, Signature: jsSignature(s.clean[i]),
				Doc: docAbove(s.raw, i, false)}
			if parentKind != "" && kind != "module" && kind != "trait" {
				sy.Parent = parent
			}
			end := blockEnd(s.bare, s.depth, i)
			if end > i {
				sy.EndLine = end + 1
			}
			f.syms = append(f.syms, sy)
			return end
		}
		if m := rsImplRe.FindStringSubmatch(l); m != nil {
			name := m[2]
			if j := strings.LastIndex(name, "::"); j >= 0 {
				name = name[j+2:]
			}
			st = append(st, container{name: name, start: i, end: blockEnd(s.bare, s.depth, i), depth: d})
			continue
		}
		if m := rsTraitRe.FindStringSubmatch(l); m != nil {
			end := sym(m[2], "trait", m[1] != "")
			st = append(st, container{name: m[2], start: i, end: end, depth: d})
			continue
		}
		if m := rsModRe.FindStringSubmatch(l); m != nil {
			end := sym(m[2], "module", m[1] != "")
			if end > i {
				st = append(st, container{name: m[2], start: i, end: end, depth: d})
			}
			continue
		}
		if m := rsFnRe.FindStringSubmatch(l); m != nil {
			kind := "func"
			if parentKind != "" {
				kind = "method"
			}
			sym(m[2], kind, m[1] != "")
			if m[2] == "main" && (strings.HasSuffix(f.rel, "src/main.rs") || strings.Contains(f.rel, "src/bin/")) {
				f.entries = append(f.entries, EntryPoint{Path: f.rel, Kind: "Rust program"})
			}
			continue
		}
		if m := rsItemRe.FindStringSubmatch(l); m != nil {
			kind := map[string]string{"struct": "struct", "enum": "enum", "union": "struct", "type": "type", "const": "const", "static": "var"}[m[2]]
			if (kind == "const" || kind == "var") && m[1] == "" {
				continue
			}
			sym(m[3], kind, m[1] != "")
		}
	}
	// Crates used: `use x::…`, `extern crate x`, and x::path in code (filtered
	// against Cargo.toml while linking).
	seen := map[string]bool{}
	bare := f.refText
	for _, re := range []*regexp.Regexp{rsUseRe, rsExternRe, rsPathRe} {
		for _, m := range re.FindAllStringSubmatch(bare, -1) {
			if !rsKeywords[m[1]] && !seen[m[1]] {
				seen[m[1]] = true
				f.externals = append(f.externals, extRef{"cargo", m[1]})
			}
		}
	}
	c := s.cleanText
	// axum: .route("/x", get(h).post(h2)) / actix: .route("/x", web::get().to(h))
	for _, m := range rsRouteRe.FindAllStringSubmatchIndex(c, -1) {
		open := strings.IndexByte(c[m[0]:], '(') + m[0]
		end := matchParen(c, open, 4000)
		if end < 0 {
			continue
		}
		args := c[m[1]:end]
		p := c[m[2]:m[3]]
		for _, am := range rsActixMRe.FindAllStringSubmatch(args, -1) {
			f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: strings.ToUpper(am[1]), Path: p, Handler: am[2],
				File: f.rel, Line: s.li.line(m[0]), Framework: "actix", Source: "code"}})
		}
		if strings.Contains(args, "web::") {
			continue
		}
		for _, am := range rsAxumMRe.FindAllStringSubmatch(args, -1) {
			method := strings.ToUpper(am[1])
			if method == "ANY" {
				method = "ANY"
			}
			f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: method, Path: p, Handler: am[2],
				File: f.rel, Line: s.li.line(m[0]), Framework: "axum", Source: "code"}})
		}
	}
	for _, m := range rsResRe.FindAllStringSubmatchIndex(c, -1) {
		stmtEnd := strings.IndexByte(c[m[1]:], ';')
		if stmtEnd < 0 || stmtEnd > 2000 {
			stmtEnd = min(2000, len(c)-m[1])
		}
		for _, am := range rsActixMRe.FindAllStringSubmatch(c[m[1]:m[1]+stmtEnd], -1) {
			f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: strings.ToUpper(am[1]), Path: c[m[2]:m[3]], Handler: am[2],
				File: f.rel, Line: s.li.line(m[0]), Framework: "actix", Source: "code"}})
		}
	}
	fw := "actix"
	if strings.Contains(c, "rocket::") || strings.Contains(c, "use rocket") {
		fw = "rocket"
	}
	for i, l := range s.clean {
		m := rsAttrRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		handler := ""
		for k := i + 1; k < len(s.clean) && k < i+8; k++ {
			if fm := rsFnRe.FindStringSubmatch(s.clean[k]); fm != nil {
				handler = fm[2]
				break
			}
		}
		f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: strings.ToUpper(m[1]), Path: m[2], Handler: handler,
			File: f.rel, Line: i + 1, Framework: fw, Source: "code"}})
	}
	addEnvMatches(f, c, s.li, rsEnvRe)
}

// --- Java / Kotlin ---

var (
	jvPackageRe = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)`)
	jvImportRe  = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([\w.]+(?:\.\*)?)`)
	jvClassRe   = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*((?:(?:public|private|protected|internal|abstract|final|static|sealed|open|data|inner|enum|annotation|value|strictfp|non-sealed)\s+)*)(class|interface|enum|record|object|@interface)\s+([A-Za-z_]\w*)`)
	jvMethodRe  = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*((?:(?:public|protected|private|static|final|abstract|synchronized|native|default|strictfp)\s+)*)(?:<[^>]+>\s+)?([\w.<>\[\],?]+(?:\s*<[^>]*>)?(?:\[\])*)\s+([a-zA-Z_]\w*)\s*\(`)
	ktFunRe     = regexp.MustCompile(`^\s*((?:(?:public|private|protected|internal|override|open|suspend|inline|operator|infix|tailrec|abstract|final|external|actual|expect)\s+)*)fun\s+(?:<[^>]+>\s+)?(?:[\w.<>?]+\.)?([A-Za-z_]\w*)\s*\(`)
	jvAnnoRe    = regexp.MustCompile(`@(RequestMapping|GetMapping|PostMapping|PutMapping|DeleteMapping|PatchMapping|Path|GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\b\s*(\(([^)]*)\))?`)
	jvMethodArg = regexp.MustCompile(`method\s*=\s*\[?\{?\s*((?:RequestMethod\.)?\w+(?:\s*,\s*(?:RequestMethod\.)?\w+)*)`)
	jvStringRe  = regexp.MustCompile(`"([^"]*)"`)
	ktorRouteRe = regexp.MustCompile(`^\s*(get|post|put|delete|patch|head|options)\s*(?:<[^>]*>)?\s*\(\s*"([^"]*)"\s*\)\s*\{`)
	ktorGroupRe = regexp.MustCompile(`^\s*route\s*\(\s*"([^"]*)"\s*\)\s*\{`)
	jvEnvRe     = regexp.MustCompile(`\bSystem\.getenv\(\s*"([^"]+)"\s*\)`)
	jvValueRe   = regexp.MustCompile(`@Value\(\s*"\$\{([A-Z][A-Z0-9_]+)(?::([^}"]*))?\}"\s*\)`)
	jvModifiers = toSet("public protected private static final abstract synchronized native default strictfp")
	jvKeywords  = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "new": true, "else": true, "throw": true, "synchronized": true, "try": true, "do": true, "assert": true, "super": true, "this": true}
)

func parseJava(f *fileInfo) {
	s := newSrcLines(string(f.content), cClean, cBare)
	f.refText = strings.Join(s.bare, "\n")
	kotlin := f.lang == "Kotlin"
	if m := jvPackageRe.FindStringSubmatch(s.cleanText); m != nil {
		f.pkg = m[1]
	}
	for _, m := range jvImportRe.FindAllStringSubmatchIndex(s.cleanText, -1) {
		f.imports = append(f.imports, rawImport{spec: s.cleanText[m[2]:m[3]], line: s.li.line(m[0])})
	}
	var st []container
	type anno struct{ name, args string }
	var pending []anno
	classPrefix := map[int]string{} // container start → prefix
	isRouteFile := strings.Contains(s.cleanText, "Mapping") || strings.Contains(s.cleanText, "@Path") || strings.Contains(s.cleanText, "javax.ws.rs") || strings.Contains(s.cleanText, "jakarta.ws.rs")
	for i := 0; i < len(s.bare); i++ {
		st = popContainers(st, i)
		l := s.bare[i]
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if isRouteFile && strings.HasPrefix(t, "@") {
			// Join a multi-line annotation from the clean view (strings kept).
			stmt := s.clean[i]
			for j := i; strings.Count(stmt, "(") > strings.Count(stmt, ")") && j+1 < len(s.clean) && j-i < 20; {
				j++
				stmt += " " + strings.TrimSpace(s.clean[j])
				i = j
			}
			for _, m := range jvAnnoRe.FindAllStringSubmatch(stmt, -1) {
				pending = append(pending, anno{m[1], m[3]})
			}
			if !jvClassRe.MatchString(s.bare[i]) && !jvMethodRe.MatchString(s.bare[i]) && !ktFunRe.MatchString(s.bare[i]) {
				continue
			}
			l = s.bare[i]
		}
		d := s.depth[i]
		var top *container
		if len(st) > 0 {
			top = &st[len(st)-1]
		}
		if m := jvClassRe.FindStringSubmatch(l); m != nil && (top == nil || d == top.depth+1) {
			kind := map[string]string{"class": "class", "interface": "interface", "enum": "enum", "record": "class", "object": "class", "@interface": "interface"}[m[2]]
			if strings.Contains(m[1], "enum") {
				kind = "enum"
			}
			exp := strings.Contains(m[1], "public") || (kotlin && !strings.Contains(m[1], "private") && !strings.Contains(m[1], "internal"))
			sy := Symbol{File: f.rel, Name: m[3], Kind: kind, Line: i + 1, Exported: exp, Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
			if top != nil {
				sy.Parent = top.name
			}
			end := blockEnd(s.bare, s.depth, i)
			if end > i {
				sy.EndLine = end + 1
			}
			f.syms = append(f.syms, sy)
			c := container{name: m[3], start: i, end: end, depth: d, exported: exp}
			for _, a := range pending {
				if a.name == "RequestMapping" || a.name == "Path" {
					if q := jvStringRe.FindStringSubmatch(a.args); q != nil {
						classPrefix[i] = q[1]
					}
				}
			}
			pending = nil
			st = append(st, c)
			continue
		}
		var name, mods string
		if kotlin {
			if m := ktFunRe.FindStringSubmatch(l); m != nil && (top == nil || d <= top.depth+1) {
				name, mods = m[2], m[1]
			}
		} else if top != nil && d == top.depth+1 {
			if m := jvMethodRe.FindStringSubmatch(l); m != nil && !jvKeywords[m[3]] && !jvKeywords[m[2]] && !jvModifiers[m[2]] && m[3] != top.name &&
				!strings.Contains(l[:strings.Index(l, m[3])], "=") {
				name, mods = m[3], m[1]
			}
		}
		if name == "" {
			if !strings.HasPrefix(t, "@") {
				pending = nil
			}
			continue
		}
		kind, parent := "func", ""
		exp := strings.Contains(mods, "public") || (kotlin && !strings.Contains(mods, "private") && !strings.Contains(mods, "internal"))
		if top != nil {
			kind, parent = "method", top.name
			if !kotlin && !strings.Contains(mods, "private") && top.exported && strings.Contains(s.clean[top.start], "interface") {
				exp = true
			}
		}
		sy := Symbol{File: f.rel, Name: name, Kind: kind, Parent: parent, Line: i + 1, Exported: exp, Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
		if end := blockEnd(s.bare, s.depth, i); end > i {
			sy.EndLine = end + 1
		}
		f.syms = append(f.syms, sy)
		if len(pending) > 0 {
			prefix := ""
			for k := len(st) - 1; k >= 0; k-- {
				if p, ok := classPrefix[st[k].start]; ok {
					prefix = p
					break
				}
			}
			var methods []string
			p, jaxrs, spring := "", false, false
			for _, a := range pending {
				switch a.name {
				case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
					methods, jaxrs = append(methods, a.name), true
				case "Path":
					if q := jvStringRe.FindStringSubmatch(a.args); q != nil {
						p = q[1]
					}
				default:
					spring = true
					if q := jvStringRe.FindStringSubmatch(a.args); q != nil {
						p = q[1]
					}
					if a.name == "RequestMapping" {
						if mm := jvMethodArg.FindStringSubmatch(a.args); mm != nil {
							for _, x := range strings.Split(mm[1], ",") {
								methods = append(methods, strings.TrimPrefix(strings.TrimSpace(x), "RequestMethod."))
							}
						}
						if len(methods) == 0 {
							methods = []string{"ANY"}
						}
					} else {
						methods = append(methods, strings.ToUpper(strings.TrimSuffix(a.name, "Mapping")))
					}
				}
			}
			if spring || jaxrs {
				fw := "spring"
				if jaxrs && !spring {
					fw = "jaxrs"
				}
				for _, m := range methods {
					f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: m, Path: joinPath(prefix, p), Handler: parent + "." + name,
						File: f.rel, Line: i + 1, Framework: fw, Source: "code"}})
				}
			}
			pending = nil
		}
	}
	if kotlin && strings.Contains(s.cleanText, "io.ktor") {
		type grp struct {
			depth  int
			prefix string
		}
		var gs []grp
		for i, l := range s.clean {
			for len(gs) > 0 && s.depth[i] < gs[len(gs)-1].depth {
				gs = gs[:len(gs)-1]
			}
			prefix := ""
			if len(gs) > 0 {
				prefix = gs[len(gs)-1].prefix
			}
			if m := ktorGroupRe.FindStringSubmatch(l); m != nil {
				gs = append(gs, grp{s.depth[i+1], joinPath(prefix, m[1])})
				continue
			}
			if m := ktorRouteRe.FindStringSubmatch(l); m != nil {
				f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: strings.ToUpper(m[1]), Path: joinPath(prefix, m[2]),
					Handler: "inline handler", File: f.rel, Line: i + 1, Framework: "ktor", Source: "code"}})
			}
		}
	}
	addEnvMatches(f, s.cleanText, s.li, jvEnvRe)
	addEnvMatches(f, s.cleanText, s.li, jvValueRe)
}

// --- C# ---

var (
	csNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([\w.]+)`)
	csTypeRe      = regexp.MustCompile(`^\s*((?:(?:public|private|protected|internal|static|abstract|sealed|partial|readonly|ref|unsafe|new|file)\s+)*)(class|interface|struct|enum|record(?:\s+struct|\s+class)?)\s+([A-Za-z_]\w*)`)
	csMethodRe    = regexp.MustCompile(`^\s*((?:(?:public|private|protected|internal|static|async|virtual|override|abstract|sealed|extern|unsafe|new|partial|readonly)\s+)+)([\w.<>\[\],?() ]+?)\s+([A-Za-z_]\w*)\s*(?:<[^>]*>)?\s*\(`)
	csAttrRe      = regexp.MustCompile(`\b(Route|HttpGet|HttpPost|HttpPut|HttpDelete|HttpPatch|HttpHead|HttpOptions)\b\s*(?:\(\s*(?:template\s*:\s*)?"([^"]*)"[^)]*\))?`)
	csMapRe       = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\.\s*Map(Get|Post|Put|Delete|Patch|Methods)\s*\(\s*"([^"]*)"`)
	csGroupRe     = regexp.MustCompile(`\bvar\s+([A-Za-z_]\w*)\s*=\s*([A-Za-z_]\w*)\s*\.\s*MapGroup\s*\(\s*"([^"]*)"\s*\)`)
	csEnvRe       = regexp.MustCompile(`\bEnvironment\.GetEnvironmentVariable\(\s*"([^"]+)"`)
	csKeywords    = map[string]bool{"if": true, "for": true, "foreach": true, "while": true, "switch": true, "catch": true, "return": true, "new": true, "using": true, "lock": true, "nameof": true, "typeof": true}
)

func parseCSharp(f *fileInfo) {
	s := newSrcLines(string(f.content), cClean, cBare)
	f.refText = strings.Join(s.bare, "\n")
	if m := csNamespaceRe.FindStringSubmatch(s.cleanText); m != nil {
		f.pkg = m[1]
	}
	var st []container
	var attrs [][2]string
	classRoute := map[int]string{}
	for i, l := range s.bare {
		st = popContainers(st, i)
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "[") {
			for _, m := range csAttrRe.FindAllStringSubmatch(s.clean[i], -1) {
				attrs = append(attrs, [2]string{m[1], m[2]})
			}
			if strings.HasSuffix(t, "]") {
				continue
			}
		}
		d := s.depth[i]
		var top *container
		if len(st) > 0 {
			top = &st[len(st)-1]
		}
		if m := csTypeRe.FindStringSubmatch(l); m != nil {
			kind := map[string]string{"class": "class", "interface": "interface", "struct": "struct", "enum": "enum"}[m[2]]
			if kind == "" {
				kind = "class"
			}
			exp := strings.Contains(m[1], "public")
			sy := Symbol{File: f.rel, Name: m[3], Kind: kind, Line: i + 1, Exported: exp, Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
			if top != nil {
				sy.Parent = top.name
			}
			end := blockEnd(s.bare, s.depth, i)
			if end > i {
				sy.EndLine = end + 1
			}
			f.syms = append(f.syms, sy)
			for _, a := range attrs {
				if a[0] == "Route" {
					classRoute[i] = strings.ReplaceAll(a[1], "[controller]", strings.TrimSuffix(m[3], "Controller"))
				}
			}
			attrs = nil
			st = append(st, container{name: m[3], start: i, end: end, depth: d, exported: exp})
			continue
		}
		if top != nil && d == top.depth+1 {
			if m := csMethodRe.FindStringSubmatch(l); m != nil && !csKeywords[m[3]] && !strings.Contains(l[:strings.Index(l, m[3])], "=") {
				sy := Symbol{File: f.rel, Name: m[3], Kind: "method", Parent: top.name, Line: i + 1, Exported: strings.Contains(m[1], "public"),
					Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
				if end := blockEnd(s.bare, s.depth, i); end > i {
					sy.EndLine = end + 1
				}
				f.syms = append(f.syms, sy)
				prefix := ""
				for k := len(st) - 1; k >= 0; k-- {
					if p, ok := classRoute[st[k].start]; ok {
						prefix = p
						break
					}
				}
				route := ""
				var methods []string
				for _, a := range attrs {
					if a[0] == "Route" {
						route = a[1]
						continue
					}
					methods = append(methods, strings.ToUpper(strings.TrimPrefix(a[0], "Http")))
					if a[1] != "" {
						route = a[1]
					}
				}
				if len(methods) == 0 && route != "" {
					methods = []string{"ANY"}
				}
				if len(methods) > 0 || prefix != "" && len(attrs) > 0 {
					p := route
					p = strings.ReplaceAll(p, "[action]", m[3])
					if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") {
						p = joinPath(prefix, p)
					}
					p = strings.TrimPrefix(p, "~")
					for _, mm := range methods {
						f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: mm, Path: p, Handler: top.name + "." + m[3],
							File: f.rel, Line: i + 1, Framework: "aspnet", Source: "code"}})
					}
				}
				attrs = nil
				continue
			}
		}
		attrs = nil
	}
	// Minimal APIs.
	c := s.cleanText
	groups := map[string]string{}
	for _, m := range csGroupRe.FindAllStringSubmatch(c, -1) {
		groups[m[1]] = joinPath(groups[m[2]], m[3])
	}
	for _, m := range csMapRe.FindAllStringSubmatchIndex(c, -1) {
		recv, verb, p := c[m[2]:m[3]], c[m[4]:m[5]], c[m[6]:m[7]]
		open := strings.Index(c[m[0]:], "(") + m[0]
		method := strings.ToUpper(verb)
		if method == "METHODS" {
			method = "ANY"
		}
		f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: method, Path: joinPath(groups[recv], p), Handler: jsHandlerText(c, open),
			File: f.rel, Line: s.li.line(m[0]), Framework: "aspnet", Source: "code"}})
	}
	addEnvMatches(f, c, s.li, csEnvRe)
}

// --- PHP ---

var (
	phpNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([\w\\]+)`)
	phpClassRe     = regexp.MustCompile(`^\s*((?:(?:abstract|final|readonly)\s+)*)(class|interface|trait|enum)\s+([A-Za-z_]\w*)`)
	phpFuncRe      = regexp.MustCompile(`^\s*((?:(?:public|private|protected|static|abstract|final)\s+)*)function\s+&?\s*([A-Za-z_]\w*)\s*\(`)
	phpRouteRe     = regexp.MustCompile(`\bRoute::(get|post|put|patch|delete|options|any|match|resource|apiResource)\s*\(`)
	phpGroupRe     = regexp.MustCompile(`(?:Route::prefix|->prefix|'prefix'\s*=>|"prefix"\s*=>)\s*\(?\s*['"]([^'"]*)['"]`)
	phpSymfonyRe   = regexp.MustCompile(`#\[Route\(\s*(?:path\s*:\s*)?['"]([^'"]*)['"]([^\]]*)\)\]`)
	phpEnvRe       = regexp.MustCompile(`\bgetenv\(\s*['"]([^'"]+)['"]\s*\)`)
	phpEnvHelperRe = regexp.MustCompile(`\benv\(\s*['"]([A-Z][A-Z0-9_]+)['"](?:\s*,\s*['"]([^'"]*)['"])?`)
	phpEnvArrRe    = regexp.MustCompile(`\$_ENV\[\s*['"]([^'"]+)['"]\s*\]`)
	phpMethodsRe   = regexp.MustCompile(`methods\s*:\s*\[([^\]]*)\]`)
	phpQuotedRe    = regexp.MustCompile(`['"]([^'"]*)['"]`)
)

func parsePHP(f *fileInfo) {
	s := newSrcLines(string(f.content), phpClean, phpBare)
	f.refText = strings.Join(s.bare, "\n")
	if m := phpNamespaceRe.FindStringSubmatch(s.cleanText); m != nil {
		f.pkg = m[1]
	}
	var st []container
	var symfonyPending [][]string
	symfonyPrefix := map[int]string{}
	for i, l := range s.bare {
		st = popContainers(st, i)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if sm := phpSymfonyRe.FindStringSubmatch(s.clean[i]); sm != nil {
			symfonyPending = append(symfonyPending, sm)
		}
		d := s.depth[i]
		var top *container
		if len(st) > 0 {
			top = &st[len(st)-1]
		}
		if m := phpClassRe.FindStringSubmatch(l); m != nil {
			kind := map[string]string{"class": "class", "interface": "interface", "trait": "trait", "enum": "enum"}[m[2]]
			sy := Symbol{File: f.rel, Name: m[3], Kind: kind, Line: i + 1, Exported: true, Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
			end := blockEnd(s.bare, s.depth, i)
			if end > i {
				sy.EndLine = end + 1
			}
			f.syms = append(f.syms, sy)
			if len(symfonyPending) > 0 {
				symfonyPrefix[i] = symfonyPending[0][1]
			}
			symfonyPending = nil
			st = append(st, container{name: m[3], start: i, end: end, depth: d, exported: true})
			continue
		}
		if m := phpFuncRe.FindStringSubmatch(l); m != nil && (top == nil && d == 0 || top != nil && d == top.depth+1) {
			kind, parent := "func", ""
			if top != nil {
				kind, parent = "method", top.name
			}
			sy := Symbol{File: f.rel, Name: m[2], Kind: kind, Parent: parent, Line: i + 1,
				Exported: !strings.Contains(m[1], "private") && !strings.Contains(m[1], "protected"), Signature: jsSignature(s.clean[i]), Doc: docAbove(s.raw, i, false)}
			if end := blockEnd(s.bare, s.depth, i); end > i {
				sy.EndLine = end + 1
			}
			f.syms = append(f.syms, sy)
			for _, sm := range symfonyPending {
				prefix := ""
				if top != nil {
					prefix = symfonyPrefix[top.start]
				}
				methods := []string{"ANY"}
				if mm := phpMethodsRe.FindStringSubmatch(sm[2]); mm != nil {
					methods = nil
					for _, q := range phpQuotedRe.FindAllStringSubmatch(mm[1], -1) {
						methods = append(methods, strings.ToUpper(q[1]))
					}
				}
				for _, mm := range methods {
					f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: mm, Path: joinPath(prefix, sm[1]), Handler: parent + "::" + m[2],
						File: f.rel, Line: i + 1, Framework: "symfony", Source: "code"}})
				}
			}
			symfonyPending = nil
		}
	}
	// Laravel routes with Route::prefix(...)->group(...) nesting.
	c := s.cleanText
	if strings.Contains(c, "Route::") {
		base := ""
		if strings.HasSuffix(f.rel, "routes/api.php") {
			base = "/api"
		}
		type grp struct {
			depth  int
			prefix string
		}
		var gs []grp
		linePrefix := make([]string, len(s.clean)+1)
		for i, l := range s.clean {
			for len(gs) > 0 && s.depth[i] < gs[len(gs)-1].depth {
				gs = gs[:len(gs)-1]
			}
			cur := base
			if len(gs) > 0 {
				cur = gs[len(gs)-1].prefix
			}
			linePrefix[i+1] = cur
			if strings.Contains(l, "group(") || strings.Contains(l, "Route::group") {
				if m := phpGroupRe.FindStringSubmatch(l); m != nil && s.depth[i+1] > s.depth[i] {
					gs = append(gs, grp{s.depth[i+1], joinPath(cur, m[1])})
				}
			}
		}
		for _, m := range phpRouteRe.FindAllStringSubmatchIndex(c, -1) {
			verb := c[m[2]:m[3]]
			open := m[1] - 1
			end := matchParen(c, open, 4000)
			if end < 0 {
				continue
			}
			args := splitTop(c[open+1:end-1], ',')
			line := s.li.line(m[0])
			prefix := linePrefix[line]
			switch verb {
			case "resource", "apiResource":
				name, ok := strLit(strings.TrimSpace(args[0]))
				if !ok {
					continue
				}
				ctrl := ""
				if len(args) > 1 {
					ctrl = strings.TrimSuffix(strings.TrimSpace(args[1]), "::class")
				}
				restRoutes(f, joinPath(prefix, name), ctrl, false, verb == "apiResource", nil, "laravel", line)
				continue
			}
			methods := []string{strings.ToUpper(verb)}
			pi := 0
			if verb == "match" {
				methods = nil
				for _, q := range phpQuotedRe.FindAllStringSubmatch(args[0], -1) {
					methods = append(methods, strings.ToUpper(q[1]))
				}
				pi = 1
			}
			if verb == "any" {
				methods = []string{"ANY"}
			}
			if pi >= len(args) {
				continue
			}
			p, ok := strLit(strings.TrimSpace(args[pi]))
			if !ok {
				continue
			}
			handler := ""
			if pi+1 < len(args) {
				h := strings.TrimSpace(args[pi+1])
				switch {
				case strings.HasPrefix(h, "["):
					parts := splitTop(strings.Trim(h, "[]"), ',')
					if len(parts) == 2 {
						act, _ := strLit(strings.TrimSpace(parts[1]))
						handler = strings.TrimSuffix(strings.TrimSpace(parts[0]), "::class") + "@" + act
					}
				case strings.HasPrefix(h, "function") || strings.HasPrefix(h, "fn"):
					handler = "inline handler"
				default:
					if q, ok := strLit(h); ok {
						handler = q
					} else {
						handler = clip(h, 120)
					}
				}
			}
			for _, mm := range methods {
				f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: mm, Path: joinPath(prefix, p), Handler: handler,
					File: f.rel, Line: line, Framework: "laravel", Source: "code"}})
			}
		}
	}
	addEnvMatches(f, c, s.li, phpEnvRe)
	addEnvMatches(f, c, s.li, phpEnvHelperRe)
	addEnvMatches(f, c, s.li, phpEnvArrRe)
}

// --- Ruby ---

var (
	rbClassRe     = regexp.MustCompile(`^(\s*)(class|module)\s+([A-Z][\w:]*)`)
	rbDefRe       = regexp.MustCompile(`^(\s*)def\s+(self\.)?([\w?!=\[\]]+)`)
	rbRequireRe   = regexp.MustCompile(`(?m)^\s*require\s*\(?\s*['"]([^'"]+)['"]`)
	rbRequireRelR = regexp.MustCompile(`(?m)^\s*require_relative\s*\(?\s*['"]([^'"]+)['"]`)
	rbVerbRe      = regexp.MustCompile(`^\s*(get|post|put|patch|delete|match)\s*\(?\s*['"]([^'"]*)['"](.*)$`)
	rbResRe       = regexp.MustCompile(`^\s*(resources|resource)\s*\(?\s*:(\w+)(.*)$`)
	rbNsRe        = regexp.MustCompile(`^\s*namespace\s*\(?\s*:(\w+)`)
	rbScopeRe     = regexp.MustCompile(`^\s*scope\s*\(?\s*(?:path:\s*)?['"]([^'"]*)['"]`)
	rbRootRe      = regexp.MustCompile(`^\s*root\s*\(?\s*(?:to:\s*|:to\s*=>\s*)?['"]([^'"]+)['"]`)
	rbToRe        = regexp.MustCompile(`(?:to:\s*|=>\s*)['"]([^'"]+)['"]`)
	rbViaRe       = regexp.MustCompile(`via:\s*(\[[^\]]*\]|:\w+)`)
	rbOnlyRe      = regexp.MustCompile(`(only|except):\s*(\[[^\]]*\]|:\w+)`)
	rbSymRe       = regexp.MustCompile(`:(\w+)`)
	rbEnvRe       = regexp.MustCompile(`\bENV\[\s*['"]([^'"]+)['"]\s*\]`)
	rbEnvFetchRe  = regexp.MustCompile(`\bENV\.fetch\(\s*['"]([^'"]+)['"](?:\s*,\s*['"]([^'"]*)['"])?`)
)

func parseRuby(f *fileInfo) {
	src := string(f.content)
	clean := cleanCode(src, rubyClean)
	f.refText = clean
	lines := strings.Split(clean, "\n")
	raw := strings.Split(src, "\n")
	li := newLineIndex(clean)
	indentOf := func(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }
	endAt := func(i, ind int) int {
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			if indentOf(lines[j]) <= ind {
				if t == "end" || strings.HasPrefix(t, "end ") || strings.HasPrefix(t, "end.") {
					return j
				}
				return j - 1
			}
		}
		return len(lines) - 1
	}
	type ent struct {
		ind     int
		name    string
		private bool
	}
	var st []ent
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		ind := indentOf(l)
		for len(st) > 0 && ind <= st[len(st)-1].ind && !strings.HasPrefix(t, "end") {
			st = st[:len(st)-1]
		}
		if (t == "private" || t == "protected") && len(st) > 0 {
			st[len(st)-1].private = true
			continue
		}
		if m := rbClassRe.FindStringSubmatch(l); m != nil {
			kind := "class"
			if m[2] == "module" {
				kind = "module"
			}
			sy := Symbol{File: f.rel, Name: m[3], Kind: kind, Line: i + 1, Exported: true, Signature: clip(t, 160), Doc: docAbove(raw, i, true)}
			if len(st) > 0 {
				sy.Parent = st[len(st)-1].name
			}
			if e := endAt(i, ind); e > i {
				sy.EndLine = e + 1
			}
			f.syms = append(f.syms, sy)
			st = append(st, ent{ind: ind, name: m[3]})
			continue
		}
		if m := rbDefRe.FindStringSubmatch(l); m != nil {
			kind, parent, priv := "func", "", false
			if len(st) > 0 {
				kind, parent, priv = "method", st[len(st)-1].name, st[len(st)-1].private
			}
			sy := Symbol{File: f.rel, Name: m[3], Kind: kind, Parent: parent, Line: i + 1, Exported: !priv && !strings.HasPrefix(m[3], "_"),
				Signature: clip(t, 160), Doc: docAbove(raw, i, true)}
			if !strings.Contains(t, "=") || strings.Contains(t, "(") {
				if e := endAt(i, ind); e > i {
					sy.EndLine = e + 1
				}
			}
			f.syms = append(f.syms, sy)
		}
	}
	for _, m := range rbRequireRe.FindAllStringSubmatchIndex(clean, -1) {
		spec := clean[m[2]:m[3]]
		f.imports = append(f.imports, rawImport{spec: spec, line: li.line(m[0])})
		f.externals = append(f.externals, extRef{"gem", strings.SplitN(spec, "/", 2)[0]})
	}
	for _, m := range rbRequireRelR.FindAllStringSubmatchIndex(clean, -1) {
		spec := clean[m[2]:m[3]]
		p := cleanRel(path.Join(f.dir, spec))
		if !strings.HasSuffix(p, ".rb") {
			p += ".rb"
		}
		f.imports = append(f.imports, rawImport{spec: spec, line: li.line(m[0]), target: p})
	}
	if strings.HasSuffix(f.rel, "config/routes.rb") || strings.Contains(f.rel, "config/routes/") {
		railsRoutes(f, lines)
	} else if strings.Contains(clean, "sinatra") || strings.Contains(clean, "Sinatra::") {
		for i, l := range lines {
			if m := rbVerbRe.FindStringSubmatch(l); m != nil && strings.Contains(m[3], "do") && strings.HasPrefix(m[2], "/") {
				f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: strings.ToUpper(m[1]), Path: m[2], Handler: "inline handler",
					File: f.rel, Line: i + 1, Framework: "sinatra", Source: "code"}})
			}
		}
	}
	addEnvMatches(f, clean, li, rbEnvRe)
	addEnvMatches(f, clean, li, rbEnvFetchRe)
}

// railsRoutes reads config/routes.rb with a namespace/scope/resources stack.
func railsRoutes(f *fileInfo, lines []string) {
	type ent struct {
		ind  int
		full string // prefix for routes inside
		res  string // resource base for member/collection
	}
	var st []ent
	indentOf := func(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		ind := indentOf(l)
		if t == "end" || strings.HasPrefix(t, "end ") {
			for len(st) > 0 && st[len(st)-1].ind >= ind {
				st = st[:len(st)-1]
			}
			continue
		}
		cur, res := "", ""
		if len(st) > 0 {
			cur, res = st[len(st)-1].full, st[len(st)-1].res
		}
		opensBlock := strings.HasSuffix(t, " do") || strings.Contains(t, " do |")
		push := func(full, res string) {
			if opensBlock {
				st = append(st, ent{ind: ind, full: full, res: res})
			}
		}
		if m := rbNsRe.FindStringSubmatch(t); m != nil {
			push(joinPath(cur, "/"+m[1]), "")
			continue
		}
		if m := rbScopeRe.FindStringSubmatch(t); m != nil {
			push(joinPath(cur, m[1]), "")
			continue
		}
		if m := rbResRe.FindStringSubmatch(t); m != nil {
			singular := m[1] == "resource"
			base := joinPath(cur, "/"+m[2])
			var filter func(string) bool
			if om := rbOnlyRe.FindStringSubmatch(m[3]); om != nil {
				set := map[string]bool{}
				for _, s := range rbSymRe.FindAllStringSubmatch(om[2], -1) {
					set[s[1]] = true
				}
				only := om[1] == "only"
				filter = func(a string) bool { return set[a] == only }
			}
			restRoutes(f, base, m[2], singular, false, filter, "rails", i+1)
			nested := base + "/{" + strings.TrimSuffix(m[2], "s") + "_id}"
			if singular {
				nested = base
			}
			push(nested, base)
			continue
		}
		if t == "member do" && res != "" {
			push(res+"/{id}", res)
			continue
		}
		if t == "collection do" && res != "" {
			push(res, res)
			continue
		}
		if m := rbRootRe.FindStringSubmatch(t); m != nil {
			f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: "GET", Path: joinPath(cur, "/"), Handler: m[1],
				File: f.rel, Line: i + 1, Framework: "rails", Source: "code"}})
			continue
		}
		if m := rbVerbRe.FindStringSubmatch(t); m != nil {
			methods := []string{strings.ToUpper(m[1])}
			if m[1] == "match" {
				methods = []string{"ANY"}
				if vm := rbViaRe.FindStringSubmatch(m[3]); vm != nil {
					methods = nil
					for _, s := range rbSymRe.FindAllStringSubmatch(vm[1], -1) {
						methods = append(methods, strings.ToUpper(s[1]))
					}
				}
			}
			handler := ""
			if tm := rbToRe.FindStringSubmatch(m[3]); tm != nil {
				handler = tm[1]
			}
			for _, mm := range methods {
				f.endpoints = append(f.endpoints, epRaw{Endpoint: Endpoint{Method: mm, Path: joinPath(cur, m[2]), Handler: handler,
					File: f.rel, Line: i + 1, Framework: "rails", Source: "code"}})
			}
			continue
		}
		if opensBlock {
			push(cur, res)
		}
	}
}
