package analyze

import (
	"bytes"
	"regexp"
	"strings"
)

// extLang maps file extensions to a language name. Data and docs formats
// (JSON, YAML, Markdown…) are deliberately absent: they are "not code".
var extLang = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".mts": "TypeScript", ".cts": "TypeScript",
	".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".vue": "Vue", ".svelte": "Svelte", ".py": "Python", ".pyi": "Python", ".rs": "Rust",
	".java": "Java", ".kt": "Kotlin", ".kts": "Kotlin", ".cs": "C#", ".php": "PHP", ".rb": "Ruby",
	".rake": "Ruby", ".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".cxx": "C++", ".hpp": "C++",
	".hh": "C++", ".m": "Objective-C", ".mm": "Objective-C", ".swift": "Swift", ".scala": "Scala",
	".dart": "Dart", ".sh": "Shell", ".bash": "Shell", ".zsh": "Shell", ".fish": "Shell",
	".ps1": "PowerShell", ".sql": "SQL", ".html": "HTML", ".htm": "HTML", ".css": "CSS",
	".scss": "SCSS", ".sass": "SCSS", ".less": "CSS", ".lua": "Lua", ".ex": "Elixir", ".exs": "Elixir",
	".erl": "Erlang", ".hs": "Haskell", ".clj": "Clojure", ".cljs": "Clojure", ".el": "Emacs Lisp",
	".nix": "Nix", ".tf": "Terraform", ".hcl": "HCL", ".proto": "Protobuf", ".graphql": "GraphQL",
	".gql": "GraphQL", ".prisma": "Prisma", ".r": "R", ".pl": "Perl", ".pm": "Perl", ".zig": "Zig",
	".jl": "Julia", ".ml": "OCaml", ".fs": "F#", ".fsx": "F#", ".vb": "Visual Basic", ".groovy": "Groovy",
	".gradle": "Groovy", ".astro": "Astro", ".templ": "Templ", ".tmpl": "", ".mk": "Makefile",
	".cmake": "CMake", ".v": "V", ".nim": "Nim", ".cr": "Crystal", ".elm": "Elm", ".gleam": "Gleam",
}

// detectLang picks a language from the file name.
func detectLang(name, ext string) string {
	switch name {
	case "Dockerfile", "Containerfile":
		return "Dockerfile"
	case "Makefile", "makefile", "GNUmakefile":
		return "Makefile"
	case "Rakefile", "Gemfile", "Guardfile", "Vagrantfile":
		return "Ruby"
	case "CMakeLists.txt":
		return "CMake"
	case "Jenkinsfile":
		return "Groovy"
	}
	if strings.HasPrefix(name, "Dockerfile.") || strings.HasSuffix(name, ".Dockerfile") || strings.HasSuffix(name, ".dockerfile") {
		return "Dockerfile"
	}
	return extLang[ext]
}

// hasParser reports whether the language has a symbol/route parser.
func hasParser(lang string) bool {
	switch lang {
	case "Go", "TypeScript", "JavaScript", "Vue", "Svelte", "Python", "Rust", "Java", "Kotlin",
		"C#", "PHP", "Ruby", "SQL", "Prisma", "Dockerfile", "Makefile":
		return true
	}
	return false
}

// importLang reports languages whose imports are resolved to files (used for orphans).
func importLang(lang string) bool {
	switch lang {
	case "TypeScript", "JavaScript", "Python":
		return true
	}
	return false
}

// family describes how a language writes comments.
type family struct {
	line       []string
	blockStart string
	blockEnd   string
}

var (
	famC      = family{line: []string{"//"}, blockStart: "/*", blockEnd: "*/"}
	famHash   = family{line: []string{"#"}}
	famSQL    = family{line: []string{"--"}, blockStart: "/*", blockEnd: "*/"}
	famHTML   = family{blockStart: "<!--", blockEnd: "-->"}
	famCSS    = family{blockStart: "/*", blockEnd: "*/"}
	famPHP    = family{line: []string{"//", "#"}, blockStart: "/*", blockEnd: "*/"}
	famLua    = family{line: []string{"--"}, blockStart: "--[[", blockEnd: "]]"}
	famHs     = family{line: []string{"--"}, blockStart: "{-", blockEnd: "-}"}
	famLisp   = family{line: []string{";"}}
	famPS     = family{line: []string{"#"}, blockStart: "<#", blockEnd: "#>"}
	famRuby   = family{line: []string{"#"}, blockStart: "=begin", blockEnd: "=end"}
	famErlang = family{line: []string{"%"}}
	famNone   = family{}
)

// familyFor picks the comment family by language, then by extension.
func familyFor(name, lang string) family {
	switch lang {
	case "Go", "TypeScript", "JavaScript", "Vue", "Svelte", "Rust", "Java", "Kotlin", "C#", "C", "C++",
		"Objective-C", "Swift", "Scala", "Dart", "Protobuf", "Groovy", "Zig", "Prisma", "Astro", "Templ",
		"F#", "V", "Gleam", "HCL", "Terraform", "Crystal":
		if lang == "Terraform" || lang == "HCL" {
			return family{line: []string{"//", "#"}, blockStart: "/*", blockEnd: "*/"}
		}
		return famC
	case "Python", "Shell", "Dockerfile", "Makefile", "R", "Perl", "Elixir", "Nix", "CMake", "GraphQL",
		"Nim", "Julia":
		return famHash
	case "Ruby":
		return famRuby
	case "PowerShell":
		return famPS
	case "SQL":
		return famSQL
	case "HTML":
		return famHTML
	case "CSS":
		return famCSS
	case "SCSS":
		return famC
	case "PHP":
		return famPHP
	case "Lua":
		return famLua
	case "Haskell", "Elm":
		return famHs
	case "Clojure", "Emacs Lisp":
		return famLisp
	case "Erlang":
		return famErlang
	}
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".yml"), strings.HasSuffix(n, ".yaml"), strings.HasSuffix(n, ".toml"),
		strings.HasPrefix(n, ".env"), strings.HasSuffix(n, ".env"), strings.HasSuffix(n, ".ini"),
		strings.HasSuffix(n, ".cfg"), strings.HasSuffix(n, ".conf"), n == ".gitignore", n == ".dockerignore":
		return famHash
	case strings.HasSuffix(n, ".xml"), strings.HasSuffix(n, ".svg"), strings.HasSuffix(n, ".md"):
		if strings.HasSuffix(n, ".md") {
			return famNone
		}
		return famHTML
	case strings.HasSuffix(n, ".jsonc"), strings.HasSuffix(n, ".json5"):
		return famC
	}
	return famNone
}

var todoRe = regexp.MustCompile(`\b(TODO|FIXME|HACK|XXX)\b`)

func hasTodo(s string) bool {
	if !strings.Contains(s, "TODO") && !strings.Contains(s, "FIXME") && !strings.Contains(s, "HACK") && !strings.Contains(s, "XXX") {
		return false
	}
	return todoRe.MatchString(s)
}

// countLines counts lines, code lines (not blank, not a full-line comment)
// and TODO-style comments.
func countLines(data []byte, fam family) (lines, code, todos int) {
	if len(data) == 0 {
		return 0, 0, 0
	}
	inBlock := false
	for len(data) > 0 {
		var raw []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			raw, data = data[:i], data[i+1:]
		} else {
			raw, data = data, nil
		}
		lines++
		t := strings.TrimSpace(string(raw))
		if inBlock {
			i := strings.Index(t, fam.blockEnd)
			if i < 0 {
				if hasTodo(t) {
					todos++
				}
				continue
			}
			if hasTodo(t[:i]) {
				todos++
			}
			inBlock = false
			t = strings.TrimSpace(t[i+len(fam.blockEnd):])
			if t == "" {
				continue
			}
		}
		if t == "" {
			continue
		}
		isComment := false
		for _, m := range fam.line {
			if strings.HasPrefix(t, m) {
				isComment = true
				break
			}
		}
		if isComment {
			if hasTodo(t) {
				todos++
			}
			continue
		}
		if fam.blockStart != "" && strings.HasPrefix(t, fam.blockStart) {
			rest := t[len(fam.blockStart):]
			j := strings.Index(rest, fam.blockEnd)
			if j < 0 {
				inBlock = true
				if hasTodo(rest) {
					todos++
				}
				continue
			}
			if hasTodo(rest[:j]) {
				todos++
			}
			after := strings.TrimSpace(rest[j+len(fam.blockEnd):])
			if after == "" {
				continue
			}
			t = after
		}
		code++
		// A trailing comment on a code line.
		for _, m := range fam.line {
			if i := strings.Index(t, m); i > 0 {
				if hasTodo(t[i:]) {
					todos++
				}
				goto next
			}
		}
		if fam.blockStart != "" {
			if i := strings.Index(t, fam.blockStart); i > 0 {
				rest := t[i:]
				if hasTodo(rest) {
					todos++
				}
				if !strings.Contains(rest[len(fam.blockStart):], fam.blockEnd) {
					inBlock = true
				}
			}
		}
	next:
	}
	return lines, code, todos
}

var testDirs = map[string]bool{"tests": true, "test": true, "__tests__": true, "spec": true, "__mocks__": true, "e2e": true}

// isTestPath detects test files by name and folder.
func isTestPath(rel, base string) bool {
	lb := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lb, "_test.go"),
		strings.HasPrefix(lb, "test_") && strings.HasSuffix(lb, ".py"),
		strings.HasSuffix(lb, "_test.py"),
		strings.HasSuffix(lb, "_spec.rb"), strings.HasSuffix(lb, "_test.rb"),
		strings.Contains(lb, ".spec."), strings.Contains(lb, ".test."),
		strings.HasSuffix(base, "Test.java"), strings.HasSuffix(base, "Tests.java"),
		strings.HasSuffix(base, "Test.kt"), strings.HasSuffix(base, "Tests.cs"), strings.HasSuffix(base, "Test.cs"),
		strings.HasSuffix(base, "Test.php"), lb == "conftest.py":
		return true
	}
	if strings.Contains("/"+rel, "/src/test/") {
		return true
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if testDirs[p] {
			return true
		}
	}
	return false
}

var lockFiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "go.sum": true,
	"Cargo.lock": true, "poetry.lock": true, "composer.lock": true, "Gemfile.lock": true,
	"Pipfile.lock": true, "npm-shrinkwrap.json": true, "uv.lock": true, "flake.lock": true,
	"packages.lock.json": true, "pdm.lock": true, "mix.lock": true, "pubspec.lock": true,
	"deno.lock": true, "bun.lock": true,
}

// isGeneratedName detects generated files by name.
func isGeneratedName(rel, base string) bool {
	lb := strings.ToLower(base)
	if lockFiles[base] {
		return true
	}
	for _, s := range []string{".pb.go", ".pb.gw.go", "_pb2.py", "_pb2_grpc.py", ".min.js", ".min.css",
		".min.mjs", ".g.dart", ".freezed.dart", ".designer.cs", ".g.cs", "_generated.go", ".gen.go",
		".pb.ts", "_pb.js", "_pb.d.ts", ".generated.ts", ".generated.js", ".map"} {
		if strings.HasSuffix(lb, s) {
			return true
		}
	}
	return strings.HasPrefix(lb, "zz_generated")
}

var genHeaderRe = regexp.MustCompile(`(?i)(Code generated .* DO NOT EDIT|@generated|<auto-generated|autogenerated file|auto-generated file|this file (is|was) (automatically|auto) generated|generated by the protocol buffer compiler)`)

// isGeneratedHeader looks for "generated" markers near the top of a file.
func isGeneratedHeader(head []byte) bool {
	if len(head) > 4096 {
		head = head[:4096]
	}
	if !bytes.Contains(bytes.ToLower(head), []byte("generated")) {
		return false
	}
	return genHeaderRe.Match(head)
}
