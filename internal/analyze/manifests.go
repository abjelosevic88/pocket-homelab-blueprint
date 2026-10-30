package analyze

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// extKey normalises a package name per ecosystem so imports and manifest
// entries meet.
func extKey(eco, name string) string {
	switch eco {
	case "pypi":
		name = pyNorm(name)
	case "cargo":
		name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	case "gem", "nuget", "composer":
		name = strings.ToLower(name)
	}
	return eco + "\x00" + name
}

// declare records a dependency listed in a manifest.
func (a *analyzer) declare(eco, name, version string, dev bool, manifest string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	k := extKey(eco, name)
	if e, ok := a.extDeclared[k]; ok {
		if e.Dev && !dev {
			e.Dev = false
		}
		if e.Version == "" {
			e.Version = version
		}
		return
	}
	a.extDeclared[k] = &External{Name: name, Ecosystem: eco, Version: strings.TrimSpace(version), Dev: dev, Declared: manifest}
}

// manifests reads every dependency manifest (files are in path order, so
// the first manifest that lists a package is the one reported).
func (a *analyzer) manifests() {
	for _, f := range a.files {
		if f.content == nil {
			continue
		}
		lb := strings.ToLower(f.base)
		matched := true
		switch {
		case f.base == "go.mod":
			a.goModDeps(f)
		case f.base == "package.json":
			a.packageJSON(f)
		case strings.HasPrefix(lb, "requirements") && strings.HasSuffix(lb, ".txt"),
			path.Base(f.dir) == "requirements" && strings.HasSuffix(lb, ".txt"):
			a.requirementsTxt(f)
		case f.base == "pyproject.toml":
			a.pyproject(f)
		case f.base == "Pipfile":
			a.tomlDeps(f, "pypi", map[string]bool{"packages": false, "dev-packages": true})
		case f.base == "Cargo.toml":
			a.cargoToml(f)
		case f.base == "composer.json":
			a.composerJSON(f)
		case f.base == "Gemfile":
			a.gemfile(f)
		case strings.HasSuffix(lb, ".csproj") || strings.HasSuffix(lb, ".fsproj") || f.base == "Directory.Packages.props":
			a.csproj(f)
		case f.base == "pom.xml":
			a.pomXML(f)
		case f.base == "build.gradle" || f.base == "build.gradle.kts":
			a.gradle(f)
		default:
			matched = false
		}
		if matched {
			f.parsed = true
		}
	}
}

var goRequireRe = regexp.MustCompile(`^\s*([^\s()]+)\s+(v[^\s]+)`)

func (a *analyzer) goModDeps(f *fileInfo) {
	in := false
	for _, line := range strings.Split(string(f.content), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "require ("):
			in = true
			continue
		case in && t == ")":
			in = false
			continue
		case strings.HasPrefix(t, "require "):
			t = strings.TrimPrefix(t, "require ")
		case !in:
			continue
		}
		if m := goRequireRe.FindStringSubmatch(t); m != nil {
			a.declare("go", m[1], m[2], false, f.rel)
		}
	}
}

// orderedStrings decodes a JSON object of strings keeping key order.
func orderedStrings(raw json.RawMessage) [][2]string {
	var out [][2]string
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return out
		}
		k, _ := kt.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return out
		}
		if s, ok := v.(string); ok {
			out = append(out, [2]string{k, s})
		}
	}
	return out
}

func (a *analyzer) packageJSON(f *fileInfo) {
	var pj struct {
		Main                 string            `json:"main"`
		Bin                  json.RawMessage   `json:"bin"`
		Scripts              json.RawMessage   `json:"scripts"`
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal(f.content, &pj) != nil {
		return
	}
	for _, set := range []struct {
		m   map[string]string
		dev bool
	}{{pj.Dependencies, false}, {pj.PeerDependencies, false}, {pj.OptionalDependencies, false}, {pj.DevDependencies, true}} {
		for _, k := range sortedKeys(set.m) {
			if _, internal := a.ts.pkgByName[k]; internal {
				continue // a workspace package of this repo
			}
			a.declare("npm", k, set.m[k], set.dev, f.rel)
		}
	}
	for i, kv := range orderedStrings(pj.Scripts) {
		if i >= 15 {
			break
		}
		a.entries = append(a.entries, EntryPoint{Path: f.rel, Kind: "npm script", Detail: clip(kv[0]+": "+kv[1], 160)})
	}
	if pj.Main != "" {
		a.entries = append(a.entries, EntryPoint{Path: cleanRel(path.Join(f.dir, pj.Main)), Kind: "Node main", Detail: "main"})
	}
	var bin string
	if json.Unmarshal(pj.Bin, &bin) == nil && bin != "" {
		a.entries = append(a.entries, EntryPoint{Path: cleanRel(path.Join(f.dir, bin)), Kind: "Node main", Detail: "bin"})
	} else {
		for _, kv := range orderedStrings(pj.Bin) {
			a.entries = append(a.entries, EntryPoint{Path: cleanRel(path.Join(f.dir, kv[1])), Kind: "Node main", Detail: "bin: " + kv[0]})
		}
	}
}

var (
	reqLineRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._\-]*)\s*(\[[^\]]*\])?\s*(.*)$`)
	tomlKVRe  = regexp.MustCompile(`^["']?([A-Za-z0-9][A-Za-z0-9._\-]*)["']?\s*=\s*(.*)$`)
	tomlStrRe = regexp.MustCompile(`["']([^"']*)["']`)
	tomlVerRe = regexp.MustCompile(`\bversion\s*=\s*["']([^"']*)["']`)
)

// pep508 splits "requests[socks]>=2.0; python_version>'3'" into name and version.
func pep508(s string) (string, string) {
	s = strings.TrimSpace(strings.SplitN(s, ";", 2)[0])
	if i := strings.Index(s, " @ "); i > 0 {
		return strings.TrimSpace(s[:i]), ""
	}
	m := reqLineRe.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	v := strings.Trim(strings.TrimSpace(m[3]), "()")
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "==") && !strings.Contains(v, ",") {
		v = strings.TrimPrefix(v, "==")
	}
	return m[1], v
}

func (a *analyzer) requirementsTxt(f *fileInfo) {
	dev := strings.Contains(strings.ToLower(f.rel), "dev") || strings.Contains(strings.ToLower(f.rel), "test")
	for _, line := range strings.Split(string(f.content), "\n") {
		t := strings.TrimSpace(line)
		if i := strings.Index(t, " #"); i >= 0 {
			t = strings.TrimSpace(t[:i])
		}
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || (strings.Contains(t, "://") && !strings.Contains(t, " @ ")) {
			continue
		}
		if n, v := pep508(t); n != "" {
			a.declare("pypi", n, v, dev, f.rel)
		}
	}
}

// tomlSections splits a TOML file into [section] → lines (enough for
// dependency tables; not a full TOML parser).
func tomlSections(src string) [][2]string {
	var out [][2]string
	section := ""
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "[") && !strings.Contains(t, "=") {
			section = strings.Trim(t, "[] ")
			continue
		}
		// Join multi-line arrays / inline tables.
		depth := strings.Count(t, "[") + strings.Count(t, "{") - strings.Count(t, "]") - strings.Count(t, "}")
		for depth > 0 && i+1 < len(lines) {
			i++
			n := strings.TrimSpace(lines[i])
			if j := strings.Index(n, " #"); j >= 0 && !strings.ContainsAny(n[j:], `"'`) {
				n = n[:j]
			}
			t += " " + n
			depth += strings.Count(n, "[") + strings.Count(n, "{") - strings.Count(n, "]") - strings.Count(n, "}")
		}
		out = append(out, [2]string{section, t})
	}
	return out
}

// tomlDepValue reads the version of `name = "1.0"` / `name = { version = "1.0" }`.
func tomlDepValue(v string) (version string, local bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		if m := tomlVerRe.FindStringSubmatch(v); m != nil {
			version = m[1]
		}
		local = strings.Contains(v, "path =") || strings.Contains(v, "path=")
		if strings.Contains(v, "workspace") && version == "" {
			version = "workspace"
		}
		return
	}
	if s, ok := strLit(v); ok {
		return s, false
	}
	return "", false
}

func isDevGroup(g string) bool {
	g = strings.ToLower(g)
	for _, w := range []string{"dev", "test", "lint", "doc", "type", "check", "format", "ci"} {
		if strings.Contains(g, w) {
			return true
		}
	}
	return false
}

func (a *analyzer) pyproject(f *fileInfo) {
	for _, kv := range tomlSections(string(f.content)) {
		sec, line := kv[0], kv[1]
		m := tomlKVRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], m[2]
		switch {
		case sec == "project" && key == "dependencies":
			for _, s := range tomlStrRe.FindAllStringSubmatch(val, -1) {
				if n, v := pep508(s[1]); n != "" {
					a.declare("pypi", n, v, false, f.rel)
				}
			}
		case sec == "project.optional-dependencies" || sec == "dependency-groups" || (sec == "tool.uv" && key == "dev-dependencies"):
			dev := sec != "project.optional-dependencies" || isDevGroup(key)
			for _, s := range tomlStrRe.FindAllStringSubmatch(val, -1) {
				if n, v := pep508(s[1]); n != "" {
					a.declare("pypi", n, v, dev, f.rel)
				}
			}
		case sec == "tool.poetry.dependencies" || sec == "tool.poetry.dev-dependencies" ||
			strings.HasPrefix(sec, "tool.poetry.group.") && strings.HasSuffix(sec, ".dependencies"):
			if strings.EqualFold(key, "python") {
				continue
			}
			v, _ := tomlDepValue(val)
			a.declare("pypi", key, v, sec != "tool.poetry.dependencies", f.rel)
		case sec == "project.scripts" || sec == "tool.poetry.scripts":
			if s, ok := strLit(val); ok {
				a.entries = append(a.entries, EntryPoint{Path: f.rel, Kind: "Python script", Detail: clip(key+": "+s, 160)})
			}
		}
	}
}

// tomlDeps reads simple `name = version` tables (Pipfile).
func (a *analyzer) tomlDeps(f *fileInfo, eco string, sections map[string]bool) {
	for _, kv := range tomlSections(string(f.content)) {
		dev, ok := sections[kv[0]]
		if !ok {
			continue
		}
		if m := tomlKVRe.FindStringSubmatch(kv[1]); m != nil {
			v, _ := tomlDepValue(m[2])
			if v == "*" {
				v = ""
			}
			a.declare(eco, m[1], v, dev, f.rel)
		}
	}
}

func (a *analyzer) cargoToml(f *fileInfo) {
	for _, kv := range tomlSections(string(f.content)) {
		sec, line := kv[0], kv[1]
		m := tomlKVRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], m[2]
		if sec == "package" && key == "name" {
			if s, ok := strLit(val); ok {
				a.localCrates = append(a.localCrates, s)
			}
			continue
		}
		base := sec
		if i := strings.LastIndex(sec, "."); i >= 0 && strings.HasPrefix(sec, "target.") {
			base = sec[i+1:]
		}
		dev := strings.HasPrefix(base, "dev-dependencies") || strings.HasPrefix(base, "build-dependencies")
		switch {
		case base == "dependencies" || base == "dev-dependencies" || base == "build-dependencies" || sec == "workspace.dependencies":
			name := key
			if strings.HasSuffix(name, ".workspace") {
				a.declare("cargo", strings.TrimSuffix(name, ".workspace"), "workspace", dev, f.rel)
				continue
			}
			v, local := tomlDepValue(val)
			if local {
				a.localCrates = append(a.localCrates, name)
				continue
			}
			a.declare("cargo", name, v, dev, f.rel)
		case strings.HasPrefix(sec, "dependencies.") || strings.HasPrefix(sec, "dev-dependencies."):
			if key == "version" {
				if s, ok := strLit(val); ok {
					name := sec[strings.Index(sec, ".")+1:]
					a.declare("cargo", name, s, strings.HasPrefix(sec, "dev"), f.rel)
				}
			}
		}
	}
}

func (a *analyzer) composerJSON(f *fileInfo) {
	var cj struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if json.Unmarshal(f.content, &cj) != nil {
		return
	}
	for _, set := range []struct {
		m   map[string]string
		dev bool
	}{{cj.Require, false}, {cj.RequireDev, true}} {
		for _, k := range sortedKeys(set.m) {
			if k == "php" || strings.HasPrefix(k, "ext-") || strings.HasPrefix(k, "lib-") || k == "composer-plugin-api" {
				continue
			}
			a.declare("composer", k, set.m[k], set.dev, f.rel)
		}
	}
}

var (
	gemRe      = regexp.MustCompile(`^\s*gem\s+['"]([^'"]+)['"](?:\s*,\s*['"]([^'"]+)['"])?`)
	gemGroupRe = regexp.MustCompile(`^\s*group\s+(.+?)\s+do\b`)
)

func (a *analyzer) gemfile(f *fileInfo) {
	depth := 0
	devDepth := -1
	for _, line := range strings.Split(string(f.content), "\n") {
		t := strings.TrimSpace(line)
		if m := gemGroupRe.FindStringSubmatch(line); m != nil {
			depth++
			if devDepth < 0 && !strings.Contains(m[1], ":production") && !strings.Contains(m[1], ":default") {
				devDepth = depth
			}
			continue
		}
		if strings.HasSuffix(t, " do") || strings.Contains(t, " do |") {
			depth++
			continue
		}
		if t == "end" {
			if depth == devDepth {
				devDepth = -1
			}
			if depth > 0 {
				depth--
			}
			continue
		}
		if m := gemRe.FindStringSubmatch(line); m != nil {
			dev := devDepth >= 0 || strings.Contains(line, "group: :development") || strings.Contains(line, "group: :test")
			a.declare("gem", m[1], m[2], dev, f.rel)
		}
	}
}

var (
	csPkgRe    = regexp.MustCompile(`(?s)<PackageReference\s+Include="([^"]+)"(?:[^>]*?Version="([^"]+)")?[^>]*?(?:/>|>(.*?)</PackageReference>)`)
	csPkgVerRe = regexp.MustCompile(`<Version>([^<]+)</Version>`)
	csPkgVer2  = regexp.MustCompile(`(?s)<PackageVersion\s+Include="([^"]+)"[^>]*?Version="([^"]+)"`)
)

func (a *analyzer) csproj(f *fileInfo) {
	src := string(f.content)
	for _, m := range csPkgRe.FindAllStringSubmatch(src, -1) {
		v := m[2]
		if v == "" {
			if vm := csPkgVerRe.FindStringSubmatch(m[3]); vm != nil {
				v = vm[1]
			}
		}
		a.declare("nuget", m[1], v, false, f.rel)
	}
	for _, m := range csPkgVer2.FindAllStringSubmatch(src, -1) {
		a.declare("nuget", m[1], m[2], false, f.rel)
	}
}

var (
	pomDepRe   = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	pomFieldRe = regexp.MustCompile(`<(groupId|artifactId|version|scope)>\s*([^<]*?)\s*</`)
	gradleRe   = regexp.MustCompile(`\b(implementation|api|compileOnly|runtimeOnly|testImplementation|testRuntimeOnly|testCompileOnly|kapt|ksp|annotationProcessor|developmentOnly|androidTestImplementation|compile|testCompile)\s*\(?\s*['"]([^:'"\s]+):([^:'"\s]+)(?::([^'"\s]+))?['"]`)
)

func (a *analyzer) pomXML(f *fileInfo) {
	for _, m := range pomDepRe.FindAllStringSubmatch(string(f.content), -1) {
		fields := map[string]string{}
		for _, fm := range pomFieldRe.FindAllStringSubmatch(m[1], -1) {
			fields[fm[1]] = fm[2]
		}
		if fields["artifactId"] == "" {
			continue
		}
		a.declare("maven", fields["groupId"]+":"+fields["artifactId"], fields["version"], fields["scope"] == "test", f.rel)
	}
}

func (a *analyzer) gradle(f *fileInfo) {
	for _, m := range gradleRe.FindAllStringSubmatch(string(f.content), -1) {
		dev := strings.HasPrefix(m[1], "test") || strings.HasPrefix(m[1], "androidTest")
		a.declare("maven", m[2]+":"+m[3], m[4], dev, f.rel)
	}
}

// goRequireModules lists required module paths, longest first.
func (a *analyzer) goRequireModules() []string {
	var out []string
	for _, e := range a.extDeclared {
		if e.Ecosystem == "go" {
			out = append(out, e.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}
