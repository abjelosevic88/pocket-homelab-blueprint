// Package analyze reads one folder of source code and describes it: the
// files and folders, the symbols in them, which file imports which, the HTTP
// endpoints (from OpenAPI documents and from the code), database tables,
// environment variables, services, entry points and the git history.
//
// Go is read with go/parser (exact); the other languages with careful
// line-based patterns (best effort). Nothing is executed.
package analyze

// Options tune one scan. Zero values mean the defaults.
type Options struct {
	MaxFiles      int   // files read at most (default 20000)
	MaxFileBytes  int64 // larger files are counted but not parsed (default 1 MiB)
	GitMaxCommits int   // commits read for churn (default 2000, -1 = skip git)
	// Vendored keeps vendor/, third_party/ and node_modules/ (default: skipped).
	Vendored bool
}

// Progress is called while scanning: a stage name and how far along it is.
type Progress func(stage string, done, total int)

// Result is everything one scan found. Paths are slash-separated and
// relative to the scanned folder ("" = the folder itself).
type Result struct {
	Name        string       `json:"name"`
	Files       []File       `json:"files"`
	Dirs        []Dir        `json:"dirs"`
	Symbols     []Symbol     `json:"symbols"`
	Edges       []Edge       `json:"edges"` // file → file imports inside the folder
	Calls       []Call       `json:"calls"` // symbol → symbol calls (Go only)
	Externals   []External   `json:"externals"`
	Endpoints   []Endpoint   `json:"endpoints"`
	SpecFiles   []SpecFile   `json:"spec_files"`
	Tables      []Table      `json:"tables"`
	EnvVars     []EnvVar     `json:"env_vars"`
	Services    []Service    `json:"services"`
	EntryPoints []EntryPoint `json:"entry_points"`
	Languages   []LangStat   `json:"languages"`
	Cycles      [][]string   `json:"cycles"` // folders that import each other in a loop
	Warnings    []string     `json:"warnings"`
	Git         *GitInfo     `json:"git,omitempty"`
	Stats       Stats        `json:"stats"`
}

// File is one source file (or a counted non-source file such as a README).
type File struct {
	Path       string `json:"path"`
	Dir        string `json:"dir"`
	Lang       string `json:"lang"` // "Go", "TypeScript", "Python", … "" = not code
	Lines      int    `json:"lines"`
	Code       int    `json:"code"` // lines that are not blank or comment (approximate)
	Bytes      int64  `json:"bytes"`
	Package    string `json:"package,omitempty"` // Go package name, Python module, Java package
	Imports    int    `json:"imports"`           // internal files it imports
	ImportedBy int    `json:"imported_by"`       // internal files importing it
	Symbols    int    `json:"symbols"`
	Todos      int    `json:"todos"` // TODO / FIXME / HACK / XXX comments
	Test       bool   `json:"test,omitempty"`
	Generated  bool   `json:"generated,omitempty"`
	Parsed     bool   `json:"parsed"` // false when too big or an unsupported language
	// From git (zero without history).
	Churn      int    `json:"churn"` // commits that touched it
	Added      int    `json:"added"`
	Deleted    int    `json:"deleted"`
	Authors    int    `json:"authors"`
	LastChange string `json:"last_change,omitempty"` // RFC3339
}

// Dir is one folder with its files added up. Edges between folders are the
// file edges rolled up (see DependsOn / UsedBy counts).
type Dir struct {
	Path      string `json:"path"`
	Files     int    `json:"files"` // files directly in it
	Lines     int    `json:"lines"` // lines in it and below
	Lang      string `json:"lang"`  // the main language in it and below
	DependsOn int    `json:"depends_on"`
	UsedBy    int    `json:"used_by"`
}

// Symbol is a named thing defined in a file.
type Symbol struct {
	File      string `json:"file"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // func method type struct interface class enum const var component trait module
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line,omitempty"`
	Exported  bool   `json:"exported"`
	Parent    string `json:"parent,omitempty"`    // receiver type or enclosing class
	Signature string `json:"signature,omitempty"` // one line, at most ~160 chars
	Doc       string `json:"doc,omitempty"`       // first sentence of its doc comment
	Refs      int    `json:"refs"`                // other files that use it (heuristic outside Go)
}

// Edge is an import from one file to another file inside the folder.
type Edge struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Names []string `json:"names,omitempty"` // imported names when the syntax says
}

// Call is a call from one function to another (Go: from the syntax tree).
type Call struct {
	From  string `json:"from"` // "file#Symbol" or "file#Type.Method"
	To    string `json:"to"`
	Count int    `json:"count"`
}

// External is a package from outside the folder.
type External struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"` // go npm pypi cargo maven composer gem nuget
	Version   string `json:"version,omitempty"`
	Dev       bool   `json:"dev,omitempty"`
	Files     int    `json:"files"`    // files importing it
	Declared  string `json:"declared"` // the manifest that lists it ("" = only imported)
}

// Endpoint is one HTTP route.
type Endpoint struct {
	Method    string   `json:"method"` // GET POST … or ANY
	Path      string   `json:"path"`   // "/api/v1/users/{id}" (params normalised to {name})
	Handler   string   `json:"handler,omitempty"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Framework string   `json:"framework"` // chi gin echo net/http gorilla fiber express fastify hono koa nextjs fastapi flask django spring aspnet laravel rails axum actix openapi
	Source    string   `json:"source"`    // code | spec
	Params    []string `json:"params,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Tag       string   `json:"tag,omitempty"`
}

// SpecFile is an OpenAPI / Swagger document found in the folder.
type SpecFile struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Version   string `json:"version"`
	Kind      string `json:"kind"` // "OpenAPI 3.1.0", "Swagger 2.0"
	Endpoints int    `json:"endpoints"`
}

// Table is a database table from SQL migrations or an ORM schema.
type Table struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Source  string   `json:"source"` // sql prisma
	Columns []Column `json:"columns"`
}

// Column is one column of a table.
type Column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	PK      bool   `json:"pk,omitempty"`
	NotNull bool   `json:"not_null,omitempty"`
	Unique  bool   `json:"unique,omitempty"`
	Ref     string `json:"ref,omitempty"` // "users.id"
}

// EnvVar is an environment variable the code reads.
type EnvVar struct {
	Name       string   `json:"name"`
	Uses       []string `json:"uses"` // "path:line", at most 20
	Default    string   `json:"default,omitempty"`
	Documented bool     `json:"documented"` // listed in .env.example / .env.sample / docs
}

// Service is a container from a compose file.
type Service struct {
	Name      string   `json:"name"`
	Image     string   `json:"image,omitempty"`
	Build     string   `json:"build,omitempty"`
	Ports     []string `json:"ports,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	File      string   `json:"file"`
}

// EntryPoint is where a program starts.
type EntryPoint struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "Go program", "npm script", "Node main", "Python script", "Dockerfile", "Makefile"
	Detail string `json:"detail,omitempty"`
}

// LangStat adds up one language.
type LangStat struct {
	Lang  string `json:"lang"`
	Files int    `json:"files"`
	Lines int    `json:"lines"`
	Code  int    `json:"code"`
}

// GitInfo is what the history says.
type GitInfo struct {
	Branch    string   `json:"branch"`
	Head      string   `json:"head"`
	HeadDate  string   `json:"head_date"`
	HeadMsg   string   `json:"head_message"`
	Remote    string   `json:"remote,omitempty"` // origin URL without credentials
	Commits   int      `json:"commits"`          // commits read
	FirstDate string   `json:"first_date"`
	Authors   []Author `json:"authors"` // most commits first, at most 30
}

// Author is one person in the history.
type Author struct {
	Name    string `json:"name"`
	Commits int    `json:"commits"`
}

// Stats are the headline numbers.
type Stats struct {
	Files     int   `json:"files"`
	CodeFiles int   `json:"code_files"`
	Dirs      int   `json:"dirs"`
	Lines     int   `json:"lines"`
	Code      int   `json:"code"`
	Bytes     int64 `json:"bytes"`
	Symbols   int   `json:"symbols"`
	Edges     int   `json:"edges"`
	Endpoints int   `json:"endpoints"`
	Tables    int   `json:"tables"`
	EnvVars   int   `json:"env_vars"`
	Externals int   `json:"externals"`
	Todos     int   `json:"todos"`
	Tests     int   `json:"tests"`
	Skipped   int   `json:"skipped"` // files left out (limits, binaries, ignored folders)
	Cycles    int   `json:"cycles"`
	Orphans   int   `json:"orphans"` // code files nothing imports and that are no entry point or test
}
