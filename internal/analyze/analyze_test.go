package analyze

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func scan(t *testing.T, dir string) *Result {
	t.Helper()
	res, err := Run(context.Background(), dir, Options{GitMaxCommits: -1}, nil)
	if err != nil {
		t.Fatalf("Run(%s): %v", dir, err)
	}
	return res
}

func endpoint(r *Result, method, path string) *Endpoint {
	for i := range r.Endpoints {
		if r.Endpoints[i].Method == method && r.Endpoints[i].Path == path {
			return &r.Endpoints[i]
		}
	}
	return nil
}

func wantEndpoint(t *testing.T, r *Result, method, path, framework string) *Endpoint {
	t.Helper()
	e := endpoint(r, method, path)
	if e == nil {
		var got []string
		for _, x := range r.Endpoints {
			got = append(got, x.Method+" "+x.Path)
		}
		t.Fatalf("missing endpoint %s %s; have %v", method, path, got)
	}
	if framework != "" && e.Framework != framework {
		t.Errorf("%s %s: framework %q, want %q", method, path, e.Framework, framework)
	}
	return e
}

func edge(r *Result, from, to string) *Edge {
	for i := range r.Edges {
		if r.Edges[i].From == from && r.Edges[i].To == to {
			return &r.Edges[i]
		}
	}
	return nil
}

func wantEdge(t *testing.T, r *Result, from, to string, names ...string) {
	t.Helper()
	e := edge(r, from, to)
	if e == nil {
		t.Fatalf("missing edge %s -> %s", from, to)
	}
	for _, n := range names {
		found := false
		for _, x := range e.Names {
			if x == n {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s -> %s: names %v lack %q", from, to, e.Names, n)
		}
	}
}

func symbol(r *Result, file, name string) *Symbol {
	for i := range r.Symbols {
		if r.Symbols[i].File == file && r.Symbols[i].Name == name {
			return &r.Symbols[i]
		}
	}
	return nil
}

func wantSymbol(t *testing.T, r *Result, file, name, kind string) *Symbol {
	t.Helper()
	s := symbol(r, file, name)
	if s == nil {
		t.Fatalf("missing symbol %s in %s", name, file)
	}
	if s.Kind != kind {
		t.Errorf("symbol %s: kind %q, want %q", name, s.Kind, kind)
	}
	return s
}

func envVar(r *Result, name string) *EnvVar {
	for i := range r.EnvVars {
		if r.EnvVars[i].Name == name {
			return &r.EnvVars[i]
		}
	}
	return nil
}

func external(r *Result, name string) *External {
	for i := range r.Externals {
		if r.Externals[i].Name == name {
			return &r.Externals[i]
		}
	}
	return nil
}

func table(r *Result, name string) *Table {
	for i := range r.Tables {
		if r.Tables[i].Name == name {
			return &r.Tables[i]
		}
	}
	return nil
}

func column(t *Table, name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

func file(r *Result, path string) *File {
	for i := range r.Files {
		if r.Files[i].Path == path {
			return &r.Files[i]
		}
	}
	return nil
}

func hasCall(r *Result, from, to string) bool {
	for _, c := range r.Calls {
		if c.From == from && c.To == to {
			return true
		}
	}
	return false
}

func hasEntry(r *Result, path, kind, detail string) bool {
	for _, e := range r.EntryPoints {
		if e.Path == path && e.Kind == kind && (detail == "" || e.Detail == detail) {
			return true
		}
	}
	return false
}

func TestGoFixture(t *testing.T) {
	r := scan(t, "testdata/gomod")

	// Walker: .gitignore, binaries.
	for _, p := range []string{"ignored/hidden.go", "debug.log", "blob.dat"} {
		if file(r, p) != nil {
			t.Errorf("%s should have been left out", p)
		}
	}
	if r.Stats.Skipped < 1 {
		t.Errorf("Skipped = %d, want the binary counted", r.Stats.Skipped)
	}
	if f := file(r, "cmd/shop/main.go"); f == nil || f.Package != "main" || f.Lang != "Go" || !f.Parsed {
		t.Errorf("main.go file = %+v", f)
	}
	if f := file(r, "internal/server/admin.go"); f == nil || f.Todos != 1 {
		t.Errorf("admin.go todos = %+v", f)
	}

	// chi routes with nested Route/Group, helper mounts, Mount of a local
	// router and of a handler, net/http 1.22 patterns.
	e := wantEndpoint(t, r, "GET", "/healthz", "chi")
	if e.Handler != "s.handleHealth" || e.File != "internal/server/server.go" || e.Source != "code" {
		t.Errorf("healthz = %+v", e)
	}
	e = wantEndpoint(t, r, "GET", "/api/v1/users/{id}", "chi")
	if !reflect.DeepEqual(e.Params, []string{"id"}) {
		t.Errorf("params = %v", e.Params)
	}
	wantEndpoint(t, r, "POST", "/api/v1/users", "chi")
	wantEndpoint(t, r, "GET", "/api/v1/orders", "chi")
	wantEndpoint(t, r, "DELETE", "/api/v1/orders/{id}", "chi")
	wantEndpoint(t, r, "GET", "/api/v1/admin/stats", "chi")
	wantEndpoint(t, r, "GET", "/v2/ping", "chi")
	wantEndpoint(t, r, "ANY", "/static/*", "chi")
	wantEndpoint(t, r, "GET", "/legacy/{name}", "net/http")
	if endpoint(r, "GET", "/ping") != nil || endpoint(r, "GET", "/admin/stats") != nil {
		t.Error("mounted routes should only appear with their prefix")
	}

	// OpenAPI.
	e = wantEndpoint(t, r, "GET", "/v1/pets", "openapi")
	if e.Source != "spec" || e.Tag != "pets" || e.Summary != "List pets" || e.Handler != "listPets" || e.Line != 8 {
		t.Errorf("spec endpoint = %+v", e)
	}
	wantEndpoint(t, r, "POST", "/v1/pets", "openapi")
	e = wantEndpoint(t, r, "GET", "/v1/pets/{petId}", "openapi")
	if !reflect.DeepEqual(e.Params, []string{"petId"}) {
		t.Errorf("spec params = %v", e.Params)
	}
	if len(r.SpecFiles) != 1 || r.SpecFiles[0] != (SpecFile{Path: "openapi.yaml", Title: "Pet API", Version: "2.1", Kind: "OpenAPI 3.0.3", Endpoints: 3}) {
		t.Errorf("spec files = %+v", r.SpecFiles)
	}

	// Imports, cycle, calls, refs.
	wantEdge(t, r, "cmd/shop/main.go", "internal/server/server.go", "New")
	wantEdge(t, r, "cmd/shop/main.go", "internal/server/admin.go")
	wantEdge(t, r, "internal/server/server.go", "internal/store/store.go", "Open")
	wantEdge(t, r, "internal/store/store.go", "internal/server/server.go", "Version")
	if !reflect.DeepEqual(r.Cycles, [][]string{{"internal/server", "internal/store"}}) {
		t.Errorf("cycles = %v", r.Cycles)
	}
	for _, c := range [][2]string{
		{"cmd/shop/main.go#main", "cmd/shop/main.go#env"},
		{"cmd/shop/main.go#main", "internal/server/server.go#New"},
		{"internal/server/server.go#New", "internal/store/store.go#Open"},
		{"internal/server/server.go#Server.handleUser", "internal/store/store.go#Store.GetUser"},
		{"internal/server/server.go#Server.handleUser", "internal/server/server.go#helper"},
	} {
		if !hasCall(r, c[0], c[1]) {
			t.Errorf("missing call %s -> %s", c[0], c[1])
		}
	}
	for _, c := range r.Calls {
		if c.From == "cmd/shop/main.go#main" && c.To == "cmd/shop/main.go#env" && c.Count != 2 {
			t.Errorf("main -> env count = %d", c.Count)
		}
	}

	// Symbols.
	s := wantSymbol(t, r, "internal/server/server.go", "Router", "method")
	if s.Parent != "Server" || s.Signature != "func (s *Server) Router() http.Handler" || s.Doc != "Router returns all routes." || !s.Exported {
		t.Errorf("Router = %+v", s)
	}
	if s := wantSymbol(t, r, "internal/server/server.go", "New", "func"); s.Doc != "New builds a Server." || s.Refs < 1 {
		t.Errorf("New = %+v", s)
	}
	wantSymbol(t, r, "internal/server/server.go", "Server", "struct")
	if s := wantSymbol(t, r, "internal/server/server.go", "Version", "const"); s.Refs != 1 {
		t.Errorf("Version refs = %d, want 1", s.Refs)
	}
	if symbol(r, "internal/server/server.go", "pingPath") != nil {
		t.Error("unexported undocumented const should be skipped")
	}
	if s := wantSymbol(t, r, "internal/store/store.go", "GetUser", "method"); s.Refs != 1 {
		t.Errorf("GetUser refs = %d", s.Refs)
	}

	// Env vars.
	if v := envVar(r, "ADDR"); v == nil || v.Default != ":8080" || !reflect.DeepEqual(v.Uses, []string{"cmd/shop/main.go:12"}) || v.Documented {
		t.Errorf("ADDR = %+v", v)
	}
	if v := envVar(r, "DB_PATH"); v == nil || v.Default != "shop.db" {
		t.Errorf("DB_PATH = %+v", v)
	}
	if v := envVar(r, "API_KEY"); v == nil || !v.Documented || len(v.Uses) != 1 {
		t.Errorf("API_KEY = %+v", v)
	}
	if v := envVar(r, "DEBUG"); v == nil || v.Documented {
		t.Errorf("DEBUG = %+v", v)
	}
	for _, n := range []string{"PORT", "DATABASE_URL", "UNUSED_VAR"} {
		if v := envVar(r, n); v == nil || !v.Documented || len(v.Uses) != 0 || v.Uses == nil {
			t.Errorf("%s = %+v", n, v)
		}
	}

	// Tables from migrations (later files win, down files ignored).
	users := table(r, "users")
	if users == nil {
		t.Fatalf("no users table: %+v", r.Tables)
	}
	if c := column(users, "id"); c == nil || !c.PK || c.Type != "INTEGER" {
		t.Errorf("users.id = %+v", c)
	}
	if c := column(users, "email"); c == nil || !c.NotNull || !c.Unique {
		t.Errorf("users.email = %+v", c)
	}
	if column(users, "name") != nil || column(users, "full_name") == nil {
		t.Errorf("rename column not applied: %+v", users.Columns)
	}
	if c := column(users, "created_at"); c == nil || c.Type != "TEXT" {
		t.Errorf("users.created_at = %+v", c)
	}
	orders := table(r, "orders")
	if orders == nil || orders.File != "internal/store/migrations/0001_init.sql" || orders.Line != 9 || orders.Source != "sql" {
		t.Fatalf("orders = %+v", orders)
	}
	if c := column(orders, "user_id"); c == nil || c.Ref != "users.id" || !c.NotNull {
		t.Errorf("orders.user_id = %+v", c)
	}
	if c := column(orders, "total"); c == nil || c.Type != "NUMERIC(10, 2)" || !c.NotNull {
		t.Errorf("orders.total = %+v", c)
	}
	if c := column(orders, "status"); c == nil || !c.NotNull {
		t.Errorf("orders.status (ALTER ADD) = %+v", c)
	}
	if column(orders, "note") == nil {
		t.Errorf("orders.note missing: %+v", orders.Columns)
	}
	items := table(r, "order_items")
	if items == nil {
		t.Fatal("no order_items")
	}
	if c := column(items, "order_id"); c == nil || !c.PK || c.Ref != "orders.id" {
		t.Errorf("order_items.order_id = %+v", c)
	}
	if c := column(items, "sku"); c == nil || !c.PK {
		t.Errorf("order_items.sku = %+v", c)
	}
	if table(r, "tmp") != nil {
		t.Error("dropped table still listed")
	}

	// Externals.
	if x := external(r, "github.com/go-chi/chi/v5"); x == nil || x.Version != "v5.0.12" || x.Files != 2 || x.Declared != "go.mod" || x.Ecosystem != "go" {
		t.Errorf("chi = %+v", x)
	}
	if x := external(r, "golang.org/x/sync"); x == nil || x.Files != 0 {
		t.Errorf("x/sync = %+v", x)
	}

	// Config: compose, Dockerfile, Makefile, entry points.
	if len(r.Services) != 2 || r.Services[0].Name != "db" || r.Services[0].Image != "postgres:16" {
		t.Fatalf("services = %+v", r.Services)
	}
	web := r.Services[1]
	if web.Build != "." || !reflect.DeepEqual(web.Ports, []string{"8080:8080"}) || !reflect.DeepEqual(web.DependsOn, []string{"db"}) {
		t.Errorf("web = %+v", web)
	}
	if !hasEntry(r, "cmd/shop", "Go program", "") {
		t.Errorf("no Go program entry: %+v", r.EntryPoints)
	}
	if !hasEntry(r, "Dockerfile", "Dockerfile", `ENTRYPOINT ["/shop"] CMD ["serve"]`) {
		t.Errorf("no Dockerfile entry: %+v", r.EntryPoints)
	}
	if !hasEntry(r, "Makefile", "Makefile", "build, test") {
		t.Errorf("no Makefile entry: %+v", r.EntryPoints)
	}

	// Folders.
	for _, d := range r.Dirs {
		if d.Path == "internal/server" && (d.DependsOn != 1 || d.UsedBy != 2 || d.Lang != "Go" || d.Files != 2) {
			t.Errorf("internal/server dir = %+v", d)
		}
	}
	if r.Dirs[0].Path != "" || r.Dirs[0].Lines != r.Stats.Lines {
		t.Errorf("root dir = %+v (stats lines %d)", r.Dirs[0], r.Stats.Lines)
	}
	if r.Stats.Endpoints != len(r.Endpoints) || r.Stats.Cycles != 1 || r.Stats.Tables != 3 {
		t.Errorf("stats = %+v", r.Stats)
	}
}

func TestTSFixture(t *testing.T) {
	r := scan(t, "testdata/tsproj")

	wantEndpoint(t, r, "GET", "/health", "express")
	e := wantEndpoint(t, r, "GET", "/api/users/{id}", "express")
	if e.Handler != "getUser" || e.File != "src/routes/users.ts" || e.Line != 6 {
		t.Errorf("users/{id} = %+v", e)
	}
	wantEndpoint(t, r, "POST", "/api/users", "express")
	wantEndpoint(t, r, "GET", "/api/items/{id}", "nextjs")
	wantEndpoint(t, r, "DELETE", "/api/items/{id}", "nextjs")
	wantEndpoint(t, r, "POST", "/api/hello", "nextjs")
	if endpoint(r, "GET", "/{id}") != nil {
		t.Error("mounted express router should carry its prefix")
	}

	wantEdge(t, r, "src/server.ts", "src/routes/users.ts", "userRouter")
	wantEdge(t, r, "src/server.ts", "src/lib/format.ts", "formatName")
	wantEdge(t, r, "src/routes/users.ts", "src/lib/format.ts", "Greeting", "formatName")

	if x := external(r, "express"); x == nil || x.Version != "^4.19.2" || x.Files != 2 || x.Declared != "package.json" || x.Ecosystem != "npm" {
		t.Errorf("express = %+v", x)
	}
	if x := external(r, "left-pad"); x == nil || x.Files != 0 || x.Declared == "" {
		t.Errorf("left-pad = %+v", x)
	}
	if x := external(r, "react"); x == nil || x.Files != 1 || x.Declared != "" {
		t.Errorf("react = %+v", x)
	}
	if x := external(r, "typescript"); x == nil || !x.Dev {
		t.Errorf("typescript = %+v", x)
	}

	if s := wantSymbol(t, r, "src/components/Button.tsx", "Button", "component"); !s.Exported {
		t.Errorf("Button = %+v", s)
	}
	s := wantSymbol(t, r, "src/lib/format.ts", "formatName", "func")
	if !s.Exported || s.Doc != "Formats a name for display." || s.Refs != 2 || s.EndLine != 6 {
		t.Errorf("formatName = %+v", s)
	}
	wantSymbol(t, r, "src/lib/format.ts", "Greeting", "interface")
	wantSymbol(t, r, "src/lib/format.ts", "VERSION", "const")
	wantSymbol(t, r, "src/lib/format.ts", "Formatter", "class")
	if s := wantSymbol(t, r, "src/lib/format.ts", "format", "method"); s.Parent != "Formatter" {
		t.Errorf("format = %+v", s)
	}
	if s := wantSymbol(t, r, "src/routes/users.ts", "createUser", "func"); s.Doc != "Creates a user." {
		t.Errorf("createUser doc = %q", s.Doc)
	}
	if s := wantSymbol(t, r, "src/routes/users.ts", "getUser", "func"); s.Exported {
		t.Error("getUser is not exported")
	}

	if v := envVar(r, "PORT"); v == nil || v.Default != "3000" || !reflect.DeepEqual(v.Uses, []string{"src/server.ts:10"}) {
		t.Errorf("PORT = %+v", v)
	}
	if envVar(r, "VITE_API_URL") == nil {
		t.Error("import.meta.env var missing")
	}
	if !hasEntry(r, "package.json", "npm script", "dev: tsx watch src/server.ts") || !hasEntry(r, "src/server.ts", "Node main", "main") {
		t.Errorf("entries = %+v", r.EntryPoints)
	}
	if f := file(r, "src/unused.ts"); f == nil || f.ImportedBy != 0 {
		t.Errorf("unused.ts = %+v", f)
	}
	if r.Stats.Orphans != 2 { // src/unused.ts and src/components/Button.tsx
		t.Errorf("orphans = %d", r.Stats.Orphans)
	}
}

func TestPythonFixture(t *testing.T) {
	r := scan(t, "testdata/pyproj")

	wantEndpoint(t, r, "GET", "/health", "fastapi")
	e := wantEndpoint(t, r, "GET", "/api/items/{item_id}", "fastapi")
	if e.Handler != "read_item" || e.File != "app/routers/items.py" {
		t.Errorf("read_item = %+v", e)
	}
	wantEndpoint(t, r, "POST", "/api/items", "fastapi")
	if endpoint(r, "GET", "/items/{item_id}") != nil {
		t.Error("include_router prefix not applied")
	}

	wantEdge(t, r, "app/main.py", "app/config.py", "SECRET")
	wantEdge(t, r, "app/main.py", "app/routers/items.py", "items")
	wantEdge(t, r, "app/routers/items.py", "app/models.py", "Item")

	if s := wantSymbol(t, r, "app/models.py", "Item", "class"); s.Doc != "An item in the shop." || s.EndLine != 11 || s.Refs != 1 {
		t.Errorf("Item = %+v", s)
	}
	if s := wantSymbol(t, r, "app/models.py", "total", "method"); s.Parent != "Item" {
		t.Errorf("total = %+v", s)
	}
	if s := wantSymbol(t, r, "app/models.py", "_private", "func"); s.Exported {
		t.Error("_private is exported")
	}
	wantSymbol(t, r, "app/models.py", "MAX_ITEMS", "const")
	if s := wantSymbol(t, r, "app/routers/items.py", "create_item", "func"); s.Signature != "def create_item( name: str, )" {
		t.Errorf("create_item signature = %q", s.Signature)
	}
	if s := wantSymbol(t, r, "app/main.py", "health", "func"); s.Doc != "Liveness probe." {
		t.Errorf("health doc = %q", s.Doc)
	}

	if v := envVar(r, "DATABASE_URL"); v == nil || v.Default != "sqlite://" {
		t.Errorf("DATABASE_URL = %+v", v)
	}
	if v := envVar(r, "SECRET_KEY"); v == nil || v.Default != "dev" {
		t.Errorf("SECRET_KEY = %+v", v)
	}
	if x := external(r, "fastapi"); x == nil || x.Version != ">=0.110" || x.Files != 2 || x.Declared != "pyproject.toml" || x.Ecosystem != "pypi" {
		t.Errorf("fastapi = %+v", x)
	}
	if x := external(r, "pydantic"); x == nil || x.Version != "2.6.0" || x.Files != 0 {
		t.Errorf("pydantic = %+v", x)
	}
	if x := external(r, "pytest"); x == nil || !x.Dev {
		t.Errorf("pytest = %+v", x)
	}
	if !hasEntry(r, "app/main.py", "Python script", "") {
		t.Errorf("entries = %+v", r.EntryPoints)
	}
	if f := file(r, "app/routers/items.py"); f == nil || f.Package != "app.routers.items" {
		t.Errorf("items.py = %+v", f)
	}
}

// TestDeterministic: two scans give the same JSON, and every slice is
// non-nil so the JSON has [] rather than null.
func TestDeterministic(t *testing.T) {
	a, _ := json.Marshal(scan(t, "testdata/gomod"))
	b, _ := json.Marshal(scan(t, "testdata/gomod"))
	if string(a) != string(b) {
		t.Fatal("two scans differ")
	}
	empty := scan(t, t.TempDir())
	j, _ := json.Marshal(empty)
	if strings.Contains(string(j), "null") {
		t.Errorf("empty scan has null: %s", j)
	}
}

// TestOddFiles: symlinks out of the root, invalid UTF-8, minified code,
// deep nesting and limits must not break a scan.
func TestOddFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package secret\nfunc Leak() {}\n"), 0o644))
	must(os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(dir, "link.go")))
	must(os.Symlink(outside, filepath.Join(dir, "linkdir")))
	must(os.WriteFile(filepath.Join(dir, "bad.ts"), []byte("export function ok() {}\n\xff\xfe\x80 const x = '\xc3';\nimport y from './nope\xff';\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "app.min.js"), []byte("var a="+strings.Repeat("function(){return 1},", 3000)+"0;"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "wide.js"), []byte("var a="+strings.Repeat("x+", 20000)+"1;\n"), 0o644))
	deep := dir
	for i := 0; i < 40; i++ {
		deep = filepath.Join(deep, "d")
	}
	must(os.MkdirAll(deep, 0o755))
	must(os.WriteFile(filepath.Join(deep, "deep.py"), []byte("def f():\n    pass\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "big.go"), []byte("package big\n"+strings.Repeat("// filler\n", 200)), 0o644))
	must(os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\nfunc (\n"), 0o644))

	r, err := Run(context.Background(), dir, Options{GitMaxCommits: -1, MaxFileBytes: 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if file(r, "link.go") != nil || file(r, "linkdir/secret.go") != nil {
		t.Error("followed a symlink out of the root")
	}
	if f := file(r, "app.min.js"); f == nil || !f.Generated || f.Parsed {
		t.Errorf("app.min.js = %+v", f)
	}
	if f := file(r, "big.go"); f == nil || f.Parsed || f.Lines != 201 {
		t.Errorf("big.go = %+v", f)
	}
	if f := file(r, "broken.go"); f == nil || f.Parsed {
		t.Errorf("broken.go = %+v", f)
	}
	if symbol(r, "bad.ts", "ok") == nil {
		t.Error("bad.ts symbols lost")
	}
	if file(r, strings.Repeat("d/", 40)+"deep.py") == nil {
		t.Error("deep file missing")
	}
	joined := strings.Join(r.Warnings, "\n")
	if !strings.Contains(joined, "Go file could not be parsed") || !strings.Contains(joined, "larger than") {
		t.Errorf("warnings = %v", r.Warnings)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}

	r, err = Run(context.Background(), dir, Options{GitMaxCommits: -1, MaxFiles: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 2 || !strings.Contains(strings.Join(r.Warnings, " "), "Only the first 2 files") {
		t.Errorf("MaxFiles: %d files, warnings %v", len(r.Files), r.Warnings)
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, "testdata/gomod", Options{GitMaxCommits: -1}, nil); err == nil {
		t.Fatal("want an error from a cancelled context")
	}
}

func TestProgress(t *testing.T) {
	stages := map[string]bool{}
	_, err := Run(context.Background(), "testdata/pyproj", Options{GitMaxCommits: -1}, func(stage string, done, total int) {
		stages[stage] = true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"files", "parsing", "linking"} {
		if !stages[s] {
			t.Errorf("stage %q never reported", s)
		}
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"/users/:id":          "/users/{id}",
		"/users/{id:[0-9]+}":  "/users/{id}",
		"/files/{path...}":    "/files/{path}",
		"users/<int:pk>/":     "/users/{pk}/",
		"/x/{$}":              "/x/",
		"/a//b":               "/a/b",
		"/static/*":           "/static/*",
		"/p/{id:[0-9]{3}}/q":  "/p/{id}/q",
		"/api/:slug(\\d+)/x":  "/api/{slug}/x",
		"/opt/:maybe?":        "/opt/{maybe}",
		"/catch/{*rest}":      "/catch/{rest}",
		"/aspnet/{id:int}/x":  "/aspnet/{id}/x",
		"/sinatra/:a/and/:b/": "/sinatra/{a}/and/{b}/",
	} {
		if got := normPath(in); got != want {
			t.Errorf("normPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := joinPath("/api", "/"); got != "/api" {
		t.Errorf("joinPath = %q", got)
	}
	if got := stripCredentials("https://user:tok@github.com/a/b.git"); got != "https://github.com/a/b.git" {
		t.Errorf("stripCredentials = %q", got)
	}
	if o, n, ok := renamePaths("src/{old => new}/f.go"); !ok || o != "src/old/f.go" || n != "src/new/f.go" {
		t.Errorf("renamePaths = %q %q %v", o, n, ok)
	}
	if o, n, ok := renamePaths("a/{ => b}/f.go"); !ok || o != "a/f.go" || n != "a/b/f.go" {
		t.Errorf("renamePaths empty side = %q %q %v", o, n, ok)
	}
	g := &gitignore{}
	for _, l := range []string{"*.log", "/build-out/", "docs/**/draft.md", "!keep.log", "node_cache"} {
		g = mergeIgnore(g, l)
	}
	cases := map[string]bool{"a/b.log": true, "keep.log": false, "build-out": true, "x/build-out": false,
		"docs/a/b/draft.md": true, "docs/draft.md": true, "src/node_cache": true, "main.go": false}
	for p, want := range cases {
		if got := g.match(p, p == "build-out" || p == "x/build-out" || p == "src/node_cache"); got != want {
			t.Errorf("gitignore %q = %v, want %v", p, got, want)
		}
	}
}

func mergeIgnore(g *gitignore, line string) *gitignore {
	f, _ := os.CreateTemp("", "gi")
	defer os.Remove(f.Name())
	_, _ = f.WriteString(line + "\n")
	f.Close()
	n := loadGitignore(f.Name())
	g.rules = append(g.rules, n.rules...)
	return g
}

// TestSelf scans this repository as a smoke test.
func TestSelf(t *testing.T) {
	start := time.Now()
	r := scan(t, "../..")
	if r.Stats.Files < 10 || r.Stats.Symbols == 0 {
		t.Errorf("self scan looks empty: %+v", r.Stats)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("self scan took %v", d)
	}
}

func TestMiscFrameworks(t *testing.T) {
	r := scan(t, "testdata/misc")
	for _, w := range []struct{ method, path, fw string }{
		{"GET", "/ping", "gin"}, {"POST", "/api/v1/login", "gin"}, {"GET", "/api/v1/users/{id}", "gin"}, {"ANY", "/api/v1/users", "gin"},
		{"GET", "/articles/{id}", "gorilla"}, {"PUT", "/articles/{id}", "gorilla"}, {"POST", "/api/items", "gorilla"},
		{"DELETE", "/admin/users/{id}", "echo"},
		{"GET", "/login", "flask"}, {"POST", "/login", "flask"}, {"GET", "/admin/users/{user_id}", "flask"},
		{"ANY", "/articles/{year}/", "django"}, {"ANY", "/posts/{slug}/", "django"}, {"ANY", "/api/status/", "django"},
		{"GET", "/users", "axum"}, {"POST", "/users", "axum"}, {"GET", "/users/{id}", "axum"},
		{"GET", "/api/users/{id}", "spring"}, {"POST", "/api/users", "spring"}, {"GET", "/api/users/search", "spring"},
		{"GET", "/api/Orders/{id}", "aspnet"}, {"POST", "/api/Orders", "aspnet"}, {"GET", "/todos/{id}", "aspnet"},
		{"GET", "/api/user", "laravel"}, {"POST", "/api/admin/reports", "laravel"}, {"PATCH", "/api/photos/{id}", "laravel"},
		{"GET", "/about", "rails"}, {"GET", "/api/users/{user_id}/posts", "rails"},
		{"GET", "/cats/{id}", "nestjs"}, {"POST", "/cats", "nestjs"},
	} {
		found := false
		for _, e := range r.Endpoints {
			if e.Method == w.method && e.Path == w.path && e.Framework == w.fw {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s %s (%s)", w.method, w.path, w.fw)
		}
	}
	if endpoint(r, "GET", "/api/photos/{id}/edit") != nil {
		t.Error("apiResource has no edit route")
	}
	post := table(r, "Post")
	if post == nil || post.Source != "prisma" {
		t.Fatalf("prisma Post = %+v", post)
	}
	if c := column(post, "authorId"); c == nil || c.Ref != "User.id" || !c.NotNull {
		t.Errorf("Post.authorId = %+v", c)
	}
	if column(post, "author") != nil {
		t.Error("relation field listed as a column")
	}
	if c := column(table(r, "User"), "email"); c == nil || !c.Unique {
		t.Errorf("User.email = %+v", c)
	}
	for name, want := range map[string]string{"Flask": "3.0.2", "axum": "0.7", "serde": "1.0", "Serilog": "3.1.1",
		"laravel/framework": "^11.0", "rails": "~> 7.1", "org.springframework.boot:spring-boot-starter-web": "3.2.0"} {
		if x := external(r, name); x == nil || x.Version != want {
			t.Errorf("%s = %+v", name, x)
		}
	}
	if x := external(r, "rspec-rails"); x == nil || !x.Dev {
		t.Errorf("rspec-rails = %+v", x)
	}
	if x := external(r, "serde"); x == nil || x.Files != 1 {
		t.Errorf("serde files = %+v", x)
	}
	if v := envVar(r, "PORT"); v == nil || v.Default != "3000" {
		t.Errorf("rust PORT = %+v", v)
	}
	if s := wantSymbol(t, r, "rust/src/main.rs", "new", "method"); s.Parent != "User" {
		t.Errorf("rust new = %+v", s)
	}
	if symbol(r, "java/src/main/java/com/acme/User.java", "User") == nil || wantSymbol(t, r, "java/src/main/java/com/acme/User.java", "getName", "method").Parent != "User" {
		t.Error("java symbols")
	}
	for _, s := range r.Symbols {
		if s.File == "java/src/main/java/com/acme/User.java" && s.Name == "User" && s.Kind != "class" {
			t.Errorf("constructor listed: %+v", s)
		}
	}
}
