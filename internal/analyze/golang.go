package analyze

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// goModule is one go.mod (or a local replace target) inside the folder.
type goModule struct {
	dir  string // slash dir of the module root
	path string // module path
}

type goImport struct {
	path  string
	alias string // explicit name, "" = the package's own name
	line  int
}

// goTypeRef names a type: qual is a package alias (calls) or an import path
// (struct fields); "" = the same package.
type goTypeRef struct{ qual, name string }

// goRawCall is a call seen in a function body, resolved while linking.
type goRawCall struct {
	from   string // "Func" or "Type.Method"
	qual   string // package alias for pkg.Func()
	typ    goTypeRef
	hasTyp bool
	field  string // x.field.Method()
	name   string
}

// goFile is what one Go file contributes before linking.
type goFile struct {
	pkg     string
	imports []goImport
	isMain  bool
	calls   []goRawCall
	idents  map[string]struct{}             // identifiers used (not declared) in the file
	sels    map[string]struct{}             // names used as x.Name on values (method refs)
	selRefs map[[2]string]struct{}          // alias.Name selectors on imported packages
	fields  map[string]map[string]goTypeRef // struct type → field → type (qual = import path)
}

var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "max": true, "min": true, "new": true, "panic": true,
	"print": true, "println": true, "real": true, "recover": true,
}

// goFramework maps an import path to a router framework.
func goFramework(p string) string {
	switch {
	case strings.HasPrefix(p, "github.com/go-chi/chi"):
		return "chi"
	case strings.HasPrefix(p, "github.com/gorilla/mux"):
		return "gorilla"
	case strings.HasPrefix(p, "github.com/gin-gonic/gin"):
		return "gin"
	case strings.HasPrefix(p, "github.com/labstack/echo"):
		return "echo"
	case strings.HasPrefix(p, "github.com/gofiber/fiber"):
		return "fiber"
	case p == "net/http":
		return "net/http"
	}
	return ""
}

var goMajorRe = regexp.MustCompile(`^v[0-9]+$`)

// defaultGoAlias guesses a package name from its import path.
func defaultGoAlias(p string) string {
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	if goMajorRe.MatchString(last) && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	if i := strings.Index(last, ".v"); i > 0 {
		last = last[:i]
	}
	last = strings.TrimPrefix(last, "go-")
	last = strings.TrimSuffix(last, "-go")
	last = strings.TrimSuffix(last, ".go")
	return strings.ReplaceAll(strings.ReplaceAll(last, "-", ""), ".", "")
}

func isGoStd(p string) bool {
	first := p
	if i := strings.IndexByte(p, '/'); i >= 0 {
		first = p[:i]
	}
	return !strings.Contains(first, ".")
}

// prepareGo reads every go.mod (module path + local replaces).
func (a *analyzer) prepareGo() {
	for _, f := range a.files {
		if f.base != "go.mod" || f.content == nil {
			continue
		}
		mod := ""
		inReplace := false
		for _, line := range strings.Split(string(f.content), "\n") {
			t := strings.TrimSpace(line)
			if i := strings.Index(t, "//"); i >= 0 {
				t = strings.TrimSpace(t[:i])
			}
			switch {
			case strings.HasPrefix(t, "module "):
				mod = strings.Trim(strings.TrimSpace(t[7:]), `"`)
				a.goMods = append(a.goMods, goModule{dir: f.dir, path: mod})
			case t == "replace (":
				inReplace = true
			case inReplace && t == ")":
				inReplace = false
			case strings.HasPrefix(t, "replace ") || inReplace:
				t = strings.TrimPrefix(t, "replace ")
				lhs, rhs, ok := strings.Cut(t, "=>")
				if !ok {
					continue
				}
				from := strings.Fields(lhs)
				to := strings.Fields(rhs)
				if len(from) == 0 || len(to) == 0 || !(strings.HasPrefix(to[0], "./") || strings.HasPrefix(to[0], "../")) {
					continue
				}
				d := path.Join(f.dir, to[0])
				if d == "." {
					d = ""
				}
				if !strings.HasPrefix(d, "..") {
					a.goMods = append(a.goMods, goModule{dir: d, path: from[0]})
				}
			}
		}
	}
	// Longest module path first so nested modules win.
	sort.SliceStable(a.goMods, func(i, j int) bool { return len(a.goMods[i].path) > len(a.goMods[j].path) })
}

// resolveGoImport maps an import path to a folder inside the scan (goDirs =
// folders that hold Go files).
func (a *analyzer) resolveGoImport(ip string, goDirs map[string]bool) (string, bool) {
	for _, m := range a.goMods {
		var d string
		switch {
		case ip == m.path:
			d = m.dir
		case strings.HasPrefix(ip, m.path+"/"):
			d = path.Join(m.dir, ip[len(m.path)+1:])
			if m.dir == "" {
				d = ip[len(m.path)+1:]
			}
		default:
			continue
		}
		if goDirs[d] {
			return d, true
		}
	}
	return "", false
}

// parseGo reads one Go file with go/parser.
func (a *analyzer) parseGo(f *fileInfo) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.rel, f.content, parser.ParseComments|parser.SkipObjectResolution)
	if file == nil || file.Name == nil {
		f.parseErr, f.parsed = true, false
		return
	}
	if err != nil {
		f.parseErr, f.parsed = true, false
	}
	g := &goFile{
		pkg:     file.Name.Name,
		idents:  map[string]struct{}{},
		sels:    map[string]struct{}{},
		selRefs: map[[2]string]struct{}{},
		fields:  map[string]map[string]goTypeRef{},
	}
	f.goF = g
	f.pkg = g.pkg
	line := func(p token.Pos) int { return fset.Position(p).Line }

	aliases := map[string]string{} // alias → import path
	fwAlias := map[string]string{} // alias → framework
	fws := map[string]bool{}
	for _, is := range file.Imports {
		p, err := strconv.Unquote(is.Path.Value)
		if err != nil || p == "C" {
			continue
		}
		al := ""
		if is.Name != nil {
			al = is.Name.Name
		}
		g.imports = append(g.imports, goImport{path: p, alias: al, line: line(is.Pos())})
		use := al
		if use == "" {
			use = defaultGoAlias(p)
		}
		if use == "_" || use == "." {
			continue
		}
		aliases[use] = p
		if fw := goFramework(p); fw != "" {
			fwAlias[use] = fw
			fws[fw] = true
		}
	}

	// File-level string constants (for route paths and env names).
	consts := map[string]string{}
	declNames := map[*ast.Ident]bool{}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			declNames[d.Name] = true
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					declNames[sp.Name] = true
				case *ast.ValueSpec:
					for i, n := range sp.Names {
						declNames[n] = true
						if d.Tok == token.CONST && i < len(sp.Values) {
							if s, ok := goStringValue(sp.Values[i], consts); ok {
								consts[n.Name] = s
							}
						}
					}
				}
			}
		}
	}

	// Symbols.
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if name == "_" {
				continue
			}
			s := Symbol{File: f.rel, Name: name, Kind: "func", Line: line(d.Pos()), EndLine: line(d.End()),
				Exported: ast.IsExported(name), Doc: goDoc(d.Doc), Signature: goFuncSig(fset, d)}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				s.Kind = "method"
				s.Parent = recvTypeName(d.Recv.List[0].Type)
			} else if name == "main" && g.pkg == "main" {
				g.isMain = true
			}
			f.syms = append(f.syms, s)
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					if sp.Name.Name == "_" {
						continue
					}
					doc := sp.Doc
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc
					}
					kind := "type"
					switch st := sp.Type.(type) {
					case *ast.StructType:
						kind = "struct"
						a.goStructFields(f, g, sp.Name.Name, st, aliases, line)
					case *ast.InterfaceType:
						kind = "interface"
					}
					f.syms = append(f.syms, Symbol{File: f.rel, Name: sp.Name.Name, Kind: kind, Line: line(sp.Pos()),
						EndLine: line(sp.End()), Exported: ast.IsExported(sp.Name.Name), Doc: goDoc(doc),
						Signature: goTypeSig(fset, sp, kind)})
				case *ast.ValueSpec:
					doc := sp.Doc
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc
					}
					if doc == nil {
						doc = sp.Comment
					}
					for i, n := range sp.Names {
						if n.Name == "_" || !(ast.IsExported(n.Name) || doc != nil) {
							continue
						}
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						f.syms = append(f.syms, Symbol{File: f.rel, Name: n.Name, Kind: kind, Line: line(n.Pos()),
							EndLine: line(sp.End()), Exported: ast.IsExported(n.Name), Doc: goDoc(doc),
							Signature: goValueSig(fset, kind, sp, i)})
					}
				}
			}
		}
	}

	// Identifier usage (for refs). Selectors on imported packages are kept
	// apart so a local symbol is not credited for pkg.Name.
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok {
				if _, isPkg := aliases[x.Name]; isPkg {
					g.selRefs[[2]string{x.Name, v.Sel.Name}] = struct{}{}
					return false
				}
			}
			g.sels[v.Sel.Name] = struct{}{}
		case *ast.Ident:
			if !declNames[v] {
				g.idents[v.Name] = struct{}{}
			}
		}
		return true
	})
	delete(g.idents, g.pkg)

	// Function bodies: calls, env reads, routes.
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		from := fd.Name.Name
		locals := map[string]goTypeRef{}
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			rt := recvTypeName(fd.Recv.List[0].Type)
			from = rt + "." + from
			for _, n := range fd.Recv.List[0].Names {
				locals[n.Name] = goTypeRef{name: rt}
			}
		}
		addParams(locals, fd.Type.Params)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncLit:
				addParams(locals, v.Type.Params)
			case *ast.AssignStmt:
				for i, rhs := range v.Rhs {
					if i < len(v.Lhs) && len(v.Lhs) == len(v.Rhs) {
						if id, ok := v.Lhs[i].(*ast.Ident); ok {
							if t, ok := compositeType(rhs); ok {
								locals[id.Name] = t
							}
						}
					}
				}
			case *ast.ValueSpec:
				for i, nm := range v.Names {
					if v.Type != nil {
						if t, ok := goTypeOf(v.Type); ok {
							locals[nm.Name] = t
						}
					} else if i < len(v.Values) {
						if t, ok := compositeType(v.Values[i]); ok {
							locals[nm.Name] = t
						}
					}
				}
			case *ast.CallExpr:
				if e, ok := goEnvCall(v, consts); ok {
					e.line = line(v.Pos())
					f.envs = append(f.envs, e)
				}
				if rc, ok := goCallTarget(v, aliases, locals); ok {
					rc.from = from
					g.calls = append(g.calls, rc)
				}
			}
			return true
		})
		if len(fwAlias) > 0 {
			w := &goRouteWalker{f: f, fset: fset, fwAlias: fwAlias, fws: fws, aliases: aliases, consts: consts,
				fn: fd.Name.Name, done: map[*ast.CallExpr]bool{}, mounted: map[string]bool{}}
			w.walkFunc(fd)
		}
	}
	// Env reads at package level (var x = os.Getenv("X")).
	for _, d := range file.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			ast.Inspect(gd, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if e, ok := goEnvCall(c, consts); ok {
						e.line = line(c.Pos())
						f.envs = append(f.envs, e)
					}
				}
				return true
			})
		}
	}
}

// goStructFields records field types (for x.field.Method() calls) and
// `env:"NAME"` struct tags.
func (a *analyzer) goStructFields(f *fileInfo, g *goFile, typ string, st *ast.StructType, aliases map[string]string, line func(token.Pos) int) {
	if st.Fields == nil {
		return
	}
	m := map[string]goTypeRef{}
	for _, fld := range st.Fields.List {
		t, ok := goTypeOf(fld.Type)
		if ok && t.qual != "" {
			t.qual = aliases[t.qual] // alias → import path
		}
		if ok {
			if len(fld.Names) == 0 {
				m[t.name] = t
			}
			for _, n := range fld.Names {
				m[n.Name] = t
			}
		}
		if fld.Tag != nil {
			tag, err := strconv.Unquote(fld.Tag.Value)
			if err != nil {
				continue
			}
			st := reflect.StructTag(tag)
			for _, key := range []string{"env", "envconfig"} {
				v := st.Get(key)
				name, _, _ := strings.Cut(v, ",")
				if envNameRe.MatchString(name) {
					def := st.Get("envDefault")
					if def == "" {
						def = st.Get("default")
					}
					f.envs = append(f.envs, envUse{name: name, def: def, line: line(fld.Pos())})
				}
			}
		}
	}
	g.fields[typ] = m
}

func addParams(locals map[string]goTypeRef, fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, fld := range fl.List {
		if t, ok := goTypeOf(fld.Type); ok {
			for _, n := range fld.Names {
				locals[n.Name] = t
			}
		}
	}
}

// goTypeOf names the type in T, *T, pkg.T, T[X].
func goTypeOf(e ast.Expr) (goTypeRef, bool) {
	for {
		switch v := e.(type) {
		case *ast.StarExpr:
			e = v.X
			continue
		case *ast.IndexExpr:
			e = v.X
			continue
		case *ast.IndexListExpr:
			e = v.X
			continue
		case *ast.ParenExpr:
			e = v.X
			continue
		case *ast.Ident:
			return goTypeRef{name: v.Name}, true
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok {
				return goTypeRef{qual: x.Name, name: v.Sel.Name}, true
			}
		}
		return goTypeRef{}, false
	}
}

// compositeType reads the type of T{…} / &T{…}.
func compositeType(e ast.Expr) (goTypeRef, bool) {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	if cl, ok := e.(*ast.CompositeLit); ok && cl.Type != nil {
		return goTypeOf(cl.Type)
	}
	return goTypeRef{}, false
}

func goCallTarget(v *ast.CallExpr, aliases map[string]string, locals map[string]goTypeRef) (goRawCall, bool) {
	fun := v.Fun
	switch ix := fun.(type) {
	case *ast.IndexExpr:
		fun = ix.X
	case *ast.IndexListExpr:
		fun = ix.X
	}
	switch fn := fun.(type) {
	case *ast.Ident:
		if goBuiltins[fn.Name] {
			return goRawCall{}, false
		}
		return goRawCall{name: fn.Name}, true
	case *ast.SelectorExpr:
		switch x := fn.X.(type) {
		case *ast.Ident:
			if t, ok := locals[x.Name]; ok {
				return goRawCall{typ: t, hasTyp: true, name: fn.Sel.Name}, true
			}
			if _, ok := aliases[x.Name]; ok {
				return goRawCall{qual: x.Name, name: fn.Sel.Name}, true
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if t, ok := locals[id.Name]; ok {
					return goRawCall{typ: t, hasTyp: true, field: x.Sel.Name, name: fn.Sel.Name}, true
				}
			}
		}
	}
	return goRawCall{}, false
}

// goStringValue evaluates a string literal, a known constant or a + chain.
func goStringValue(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			l, ok1 := goStringValue(v.X, consts)
			r, ok2 := goStringValue(v.Y, consts)
			return l + r, ok1 && ok2
		}
	case *ast.ParenExpr:
		return goStringValue(v.X, consts)
	}
	return "", false
}

var looseEnvRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// goEnvCall recognises os.Getenv("X"), os.LookupEnv("X") and helpers like
// env("X", "default") / getEnvBool("X", false).
func goEnvCall(v *ast.CallExpr, consts map[string]string) (envUse, bool) {
	if len(v.Args) == 0 {
		return envUse{}, false
	}
	var name, pkg string
	switch fn := v.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
		if x, ok := fn.X.(*ast.Ident); ok {
			pkg = x.Name
		}
	default:
		return envUse{}, false
	}
	ln := strings.ToLower(name)
	if !strings.Contains(ln, "env") || strings.HasPrefix(ln, "set") || strings.HasPrefix(ln, "unset") ||
		strings.HasPrefix(ln, "expand") || ln == "environ" || strings.Contains(ln, "environment") && strings.HasPrefix(ln, "new") {
		return envUse{}, false
	}
	key, ok := goStringValue(v.Args[0], consts)
	if !ok {
		return envUse{}, false
	}
	if pkg == "os" && (name == "Getenv" || name == "LookupEnv") {
		if !looseEnvRe.MatchString(key) {
			return envUse{}, false
		}
	} else if !envNameRe.MatchString(key) {
		return envUse{}, false
	}
	e := envUse{name: key}
	if len(v.Args) > 1 {
		switch d := v.Args[1].(type) {
		case *ast.BasicLit:
			if d.Kind == token.STRING {
				e.def, _ = strconv.Unquote(d.Value)
			} else {
				e.def = d.Value
			}
		case *ast.Ident:
			if s, ok := consts[d.Name]; ok {
				e.def = s
			} else if d.Name == "true" || d.Name == "false" {
				e.def = d.Name
			}
		}
	}
	return e, true
}

func recvTypeName(e ast.Expr) string {
	if t, ok := goTypeOf(e); ok {
		return t.name
	}
	return ""
}

func goDoc(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return firstSentence(cg.Text())
}

func goFuncSig(fset *token.FileSet, d *ast.FuncDecl) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type})
	return clip(buf.String(), 160)
}

func goTypeSig(fset *token.FileSet, sp *ast.TypeSpec, kind string) string {
	ts := *sp
	ts.Doc, ts.Comment = nil, nil
	if kind == "struct" || kind == "interface" {
		ts.Type = ast.NewIdent(kind)
	} else if sp.Type.End()-sp.Type.Pos() > 120 {
		ts.Type = ast.NewIdent("…")
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, &ts)
	return clip("type "+buf.String(), 160)
}

func goValueSig(fset *token.FileSet, kind string, sp *ast.ValueSpec, i int) string {
	vs := &ast.ValueSpec{Names: []*ast.Ident{sp.Names[i]}, Type: sp.Type}
	if i < len(sp.Values) {
		v := sp.Values[i]
		if v.End()-v.Pos() > 100 {
			v = ast.NewIdent("…")
		}
		vs.Values = []ast.Expr{v}
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, vs)
	return clip(kind+" "+buf.String(), 160)
}

func goExprText(e ast.Expr) string {
	if _, ok := e.(*ast.FuncLit); ok {
		return "func literal"
	}
	return clip(types.ExprString(e), 120)
}

// --- routes ---

var (
	chiMethods = map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE",
		"Head": "HEAD", "Options": "OPTIONS", "Connect": "CONNECT", "Trace": "TRACE"}
	routerTypeNames = map[string]bool{"Router": true, "Mux": true, "Engine": true, "RouterGroup": true, "IRouter": true,
		"IRoutes": true, "Echo": true, "Group": true, "App": true, "ServeMux": true}
)

type pendingRoute struct {
	ep   Endpoint
	base string
}

type pendingMount struct {
	m    mountRaw
	base string
}

// goRouteWalker finds routes in one function, tracking router variables and
// their path prefixes through chi Route/Group closures, gin/echo/fiber
// groups and gorilla subrouters.
type goRouteWalker struct {
	f       *fileInfo
	fset    *token.FileSet
	fwAlias map[string]string
	fws     map[string]bool
	aliases map[string]string
	consts  map[string]string
	fn      string
	done    map[*ast.CallExpr]bool
	mounted map[string]bool
	routes  []pendingRoute
	mounts  []pendingMount
}

func (w *goRouteWalker) node(base string) string {
	n := "go:" + w.f.dir + ":" + w.fn
	if base != "" && w.mounted[base] {
		n += "#" + base
	}
	return n
}

func (w *goRouteWalker) walkFunc(fd *ast.FuncDecl) {
	scope := map[string]string{}
	w.routerParams(fd.Type.Params, scope, "")
	w.walk(fd.Body, scope)
	for _, r := range w.routes {
		w.f.endpoints = append(w.f.endpoints, epRaw{Endpoint: r.ep, node: w.node(r.base)})
	}
	for _, m := range w.mounts {
		m.m.parent = w.node(m.base)
		w.f.mounts = append(w.f.mounts, m.m)
	}
}

func (w *goRouteWalker) isRouterType(e ast.Expr) bool {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	fw, ok := w.fwAlias[x.Name]
	if !ok {
		return false
	}
	if fw == "net/http" {
		return sel.Sel.Name == "ServeMux"
	}
	return routerTypeNames[sel.Sel.Name]
}

func (w *goRouteWalker) routerParams(fl *ast.FieldList, scope map[string]string, prefix string) {
	if fl == nil {
		return
	}
	for _, fld := range fl.List {
		if w.isRouterType(fld.Type) {
			for _, n := range fld.Names {
				scope[n.Name] = prefix
			}
		}
	}
}

func (w *goRouteWalker) str(e ast.Expr) (string, bool) { return goStringValue(e, w.consts) }

func (w *goRouteWalker) line(n ast.Node) int { return w.fset.Position(n.Pos()).Line }

func (w *goRouteWalker) walk(n ast.Node, scope map[string]string) {
	ast.Inspect(n, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.AssignStmt:
			for i, rhs := range v.Rhs {
				if i < len(v.Lhs) && len(v.Lhs) == len(v.Rhs) {
					if id, ok := v.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
						if p, ok := w.routerExpr(rhs, scope); ok {
							scope[id.Name] = p
						}
					}
				}
			}
		case *ast.ValueSpec:
			for i, val := range v.Values {
				if i < len(v.Names) {
					if p, ok := w.routerExpr(val, scope); ok {
						scope[v.Names[i].Name] = p
					}
				}
			}
			if v.Type != nil && w.isRouterType(v.Type) {
				for _, nm := range v.Names {
					if _, ok := scope[nm.Name]; !ok {
						scope[nm.Name] = ""
					}
				}
			}
		case *ast.FuncLit:
			for _, fld := range v.Type.Params.List {
				if w.isRouterType(fld.Type) {
					for _, nm := range fld.Names {
						if _, ok := scope[nm.Name]; !ok {
							scope[nm.Name] = ""
						}
					}
				}
			}
		case *ast.CallExpr:
			if w.done[v] {
				return true
			}
			return w.call(v, scope)
		}
		return true
	})
}

// routerExpr reports whether e yields a router, and its prefix.
func (w *goRouteWalker) routerExpr(e ast.Expr, scope map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		p, ok := scope[v.Name]
		return p, ok
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		m := sel.Sel.Name
		if x, ok := sel.X.(*ast.Ident); ok {
			if fw, isFw := w.fwAlias[x.Name]; isFw {
				switch m {
				case "NewServeMux":
					return "", true
				case "NewRouter", "NewMux", "New", "Default":
					return "", fw != "net/http"
				}
				return "", false
			}
		}
		switch m {
		case "Group":
			base, known := w.routerExpr(sel.X, scope)
			if len(v.Args) >= 1 {
				if s, ok := w.str(v.Args[0]); ok {
					return joinPath(base, s), known
				}
			}
			return base, known
		case "With":
			return w.routerExpr(sel.X, scope)
		case "Subrouter":
			if inner, ok := sel.X.(*ast.CallExpr); ok {
				if is, ok := inner.Fun.(*ast.SelectorExpr); ok && is.Sel.Name == "PathPrefix" && len(inner.Args) == 1 {
					if s, ok := w.str(inner.Args[0]); ok {
						b, known := w.routerExpr(is.X, scope)
						return joinPath(b, s), known
					}
				}
				return w.routerExpr(inner, scope)
			}
		case "PathPrefix", "Host", "Schemes":
			return w.routerExpr(sel.X, scope)
		}
	}
	return "", false
}

// baseVar is the variable a call chain starts from (r in r.With(m).Get).
func baseVar(e ast.Expr) string {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
				e = sel.X
				continue
			}
			return ""
		default:
			return ""
		}
	}
}

func (w *goRouteWalker) qualOf(e ast.Expr, scope map[string]string) string {
	if x, ok := e.(*ast.Ident); ok {
		if _, isVar := scope[x.Name]; !isVar {
			if _, ok := w.aliases[x.Name]; ok {
				return x.Name
			}
		}
	}
	return ""
}

func (w *goRouteWalker) call(v *ast.CallExpr, scope map[string]string) bool {
	sel, ok := v.Fun.(*ast.SelectorExpr)
	if !ok {
		if id, ok := v.Fun.(*ast.Ident); ok {
			w.mountByArgs(v, id.Name, "", scope)
		}
		return true
	}
	m := sel.Sel.Name
	args := v.Args
	switch {
	case m == "Route" && len(args) == 2:
		p, ok := w.str(args[0])
		if !ok {
			return true
		}
		base, _ := w.routerExpr(sel.X, scope)
		prefix := joinPath(base, p)
		if lit, ok := args[1].(*ast.FuncLit); ok {
			ns := copyScope(scope)
			w.litParams(lit, ns, prefix)
			w.walk(lit.Body, ns)
			return false
		}
		w.addMount(sel.X, prefix, args[1], false, scope)
		return true
	case m == "Group" && len(args) == 1:
		if lit, ok := args[0].(*ast.FuncLit); ok {
			base, _ := w.routerExpr(sel.X, scope)
			ns := copyScope(scope)
			w.litParams(lit, ns, base)
			w.walk(lit.Body, ns)
			return false
		}
	case m == "Mount" && len(args) == 2:
		if p, ok := w.str(args[0]); ok {
			base, _ := w.routerExpr(sel.X, scope)
			w.addMount(sel.X, joinPath(base, p), args[1], true, scope)
		}
		return true
	case m == "Methods":
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if is, ok := inner.Fun.(*ast.SelectorExpr); ok && (is.Sel.Name == "HandleFunc" || is.Sel.Name == "Handle") {
				var methods []string
				for _, a := range args {
					if s, ok := w.str(a); ok {
						methods = append(methods, strings.ToUpper(s))
					}
				}
				w.route(inner, is, methods, "gorilla", scope)
				w.done[inner] = true
			}
		}
		return true
	}
	if w.route(v, sel, nil, "", scope) {
		return true
	}
	w.mountByArgs(v, m, w.qualOf(sel.X, scope), scope)
	return true
}

func (w *goRouteWalker) litParams(lit *ast.FuncLit, scope map[string]string, prefix string) {
	for _, fld := range lit.Type.Params.List {
		for _, n := range fld.Names {
			scope[n.Name] = prefix
		}
		break
	}
}

func copyScope(s map[string]string) map[string]string {
	n := make(map[string]string, len(s)+2)
	for k, v := range s {
		n[k] = v
	}
	return n
}

// mountByArgs: a helper that receives a router (s.apiRoutes(r)) serves its
// routes under that router's prefix.
func (w *goRouteWalker) mountByArgs(v *ast.CallExpr, fname, qual string, scope map[string]string) {
	for _, arg := range v.Args {
		id, ok := arg.(*ast.Ident)
		if !ok {
			continue
		}
		p, ok := scope[id.Name]
		if !ok {
			continue
		}
		w.mounts = append(w.mounts, pendingMount{base: id.Name, m: mountRaw{prefix: p, line: w.line(v), goFunc: fname, goQual: qual}})
		return
	}
}

func (w *goRouteWalker) addMount(baseX ast.Expr, prefix string, target ast.Expr, explicit bool, scope map[string]string) {
	m := mountRaw{prefix: prefix, line: w.line(target), explicit: explicit, handler: goExprText(target), fw: w.fwFor("Mount", baseX)}
	switch t := target.(type) {
	case *ast.Ident:
		if _, ok := scope[t.Name]; ok {
			w.mounted[t.Name] = true
			m.child = "go:" + w.f.dir + ":" + w.fn + "#" + t.Name
		} else {
			m.goFunc = t.Name
		}
	case *ast.SelectorExpr:
		m.goFunc, m.goQual = t.Sel.Name, w.qualOf(t.X, scope)
	case *ast.CallExpr:
		switch fn := t.Fun.(type) {
		case *ast.Ident:
			m.goFunc = fn.Name
		case *ast.SelectorExpr:
			m.goFunc, m.goQual = fn.Sel.Name, w.qualOf(fn.X, scope)
			if w.fwAlias[m.goQual] == "net/http" {
				m.goFunc, m.goQual = "", ""
			}
		}
	}
	w.mounts = append(w.mounts, pendingMount{base: baseVar(baseX), m: m})
}

func (w *goRouteWalker) fwFor(m string, x ast.Expr) string {
	if id, ok := x.(*ast.Ident); ok && w.fwAlias[id.Name] == "net/http" {
		return "net/http"
	}
	switch {
	case httpMethods[m] || m == "Any":
		if w.fws["echo"] && !w.fws["gin"] {
			return "echo"
		}
		return "gin"
	case m == "All":
		return "fiber"
	case chiMethods[m] != "" || m == "Method" || m == "MethodFunc" || m == "Mount" || m == "Add":
		if w.fws["fiber"] && !w.fws["chi"] {
			return "fiber"
		}
		if w.fws["echo"] && m == "Add" {
			return "echo"
		}
		if !w.fws["chi"] && w.fws["gorilla"] {
			return "gorilla"
		}
		if !w.fws["chi"] && w.fws["net/http"] && !w.fws["fiber"] {
			return "net/http"
		}
		return "chi"
	default: // Handle / HandleFunc
		if w.fws["gorilla"] {
			return "gorilla"
		}
		if w.fws["chi"] {
			return "chi"
		}
		if w.fws["gin"] {
			return "gin"
		}
		return "net/http"
	}
}

// route records r.Get("/x", h) and friends. Returns false when the call is
// not a route registration.
func (w *goRouteWalker) route(v *ast.CallExpr, sel *ast.SelectorExpr, methods []string, fwHint string, scope map[string]string) bool {
	m := sel.Sel.Name
	args := v.Args
	if len(args) < 2 {
		return false
	}
	var method string
	pathArg := args[0]
	pattern := false
	switch {
	case chiMethods[m] != "":
		method = chiMethods[m]
	case httpMethods[m]:
		method = m
	case m == "Any" || m == "All":
		method = "ANY"
	case (m == "Method" || m == "MethodFunc") && len(args) >= 3:
		s, ok := w.str(args[0])
		if !ok {
			return false
		}
		method, pathArg = strings.ToUpper(s), args[1]
	case (m == "Handle" || m == "Add") && len(args) >= 3:
		s, ok := w.str(args[0])
		if !ok || !httpMethods[strings.ToUpper(s)] {
			return false
		}
		method, pathArg = strings.ToUpper(s), args[1]
	case m == "HandleFunc" || m == "Handle":
		pattern = true
	default:
		return false
	}
	p, ok := w.str(pathArg)
	if !ok {
		return false
	}
	if pattern {
		mm, rest := splitMethodPattern(p)
		if mm != "" {
			method = mm
		}
		p = rest
	}
	base, known := w.routerExpr(sel.X, scope)
	if !(strings.HasPrefix(p, "/") || p == "*" || (p == "" && known)) {
		return false
	}
	fw := fwHint
	if fw == "" {
		fw = w.fwFor(m, sel.X)
	}
	full := joinPath(base, p)
	if full == "" {
		full = "/"
	}
	if len(methods) == 0 {
		if method == "" {
			method = "ANY"
		}
		methods = []string{method}
	}
	for _, mm := range methods {
		w.routes = append(w.routes, pendingRoute{base: baseVar(sel.X), ep: Endpoint{
			Method: mm, Path: full, Handler: goExprText(args[len(args)-1]), File: w.f.rel, Line: w.line(v),
			Framework: fw, Source: "code",
		}})
	}
	return true
}
