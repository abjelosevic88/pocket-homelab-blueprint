package analyze

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envExampleNames = map[string]bool{
	".env.example": true, ".env.sample": true, ".env.template": true, "example.env": true, ".env.dist": true,
	"env.example": true, "sample.env": true, ".env.defaults": true, ".env.local.example": true,
	".env.development.example": true, ".env.production.example": true, "env.sample": true, ".env.tmpl": true,
}

var (
	envLineRe   = regexp.MustCompile(`^\s*#?\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
	mdEnvRe     = regexp.MustCompile("`([A-Z][A-Z0-9_]{2,})(?:=[^`]*)?`")
	composeRe   = regexp.MustCompile(`^(docker-)?compose(\.[\w.-]+)?\.ya?ml$`)
	makeTgtRe   = regexp.MustCompile(`^([A-Za-z0-9][\w.\-/]*)\s*:`)
	dockerCmdRe = regexp.MustCompile(`(?i)^\s*(CMD|ENTRYPOINT)\s+(.*)$`)
	procfileRe  = regexp.MustCompile(`^([\w-]+)\s*:\s*(.+)$`)
)

// isEnvExample reports whether a file documents environment variables.
func isEnvExample(base string) bool {
	if envExampleNames[base] {
		return true
	}
	return strings.HasPrefix(base, ".env.") && (strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template"))
}

// keepContent reports whether a file's bytes are still needed after the
// parallel parse (manifests, config, specs, SQL); the rest is dropped early
// to keep memory flat on big folders.
func (a *analyzer) keepContent(f *fileInfo) bool {
	switch f.lang {
	case "SQL", "Prisma", "Dockerfile", "Makefile":
		return true
	}
	lb := strings.ToLower(f.base)
	switch f.base {
	case "go.mod", "package.json", "pyproject.toml", "Pipfile", "Cargo.toml", "composer.json", "Gemfile", "pom.xml",
		"build.gradle", "build.gradle.kts", "Directory.Packages.props", "Procfile":
		return true
	}
	return f.spec || isEnvExample(f.base) || composeRe.MatchString(f.base) || strings.HasSuffix(lb, ".csproj") ||
		strings.HasSuffix(lb, ".fsproj") || strings.HasSuffix(lb, ".md") ||
		strings.HasPrefix(lb, "requirements") && strings.HasSuffix(lb, ".txt") ||
		path.Base(f.dir) == "requirements" && strings.HasSuffix(lb, ".txt")
}

// config reads env examples, docs, compose files, Dockerfiles, Makefiles.
func (a *analyzer) config() {
	for _, f := range a.files {
		if f.content == nil {
			continue
		}
		switch {
		case isEnvExample(f.base):
			for _, line := range strings.Split(string(f.content), "\n") {
				if m := envLineRe.FindStringSubmatch(line); m != nil {
					a.envDocOnly[m[1]] = true
				}
			}
			f.parsed = true
		case strings.EqualFold(f.base, "README.md") || (strings.HasPrefix(f.rel, "docs/") && strings.Count(f.rel, "/") == 1 && strings.HasSuffix(strings.ToLower(f.base), ".md")):
			for _, m := range mdEnvRe.FindAllStringSubmatch(string(f.content), -1) {
				a.envDocs[m[1]] = true
			}
		case composeRe.MatchString(f.base):
			a.compose(f)
		case f.lang == "Dockerfile":
			a.dockerfile(f)
		case f.lang == "Makefile":
			a.makefile(f)
		case f.base == "Procfile":
			for _, line := range strings.Split(string(f.content), "\n") {
				if m := procfileRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
					a.entries = append(a.entries, EntryPoint{Path: f.rel, Kind: "Procfile", Detail: clip(m[1]+": "+m[2], 160)})
				}
			}
		}
	}
}

// compose reads services from a docker compose file.
func (a *analyzer) compose(f *fileInfo) {
	var doc struct {
		Services map[string]struct {
			Image       string    `yaml:"image"`
			Build       yaml.Node `yaml:"build"`
			Ports       []any     `yaml:"ports"`
			DependsOn   yaml.Node `yaml:"depends_on"`
			Environment yaml.Node `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(f.content, &doc); err != nil {
		a.warnf("%s could not be read as a compose file", f.rel)
		return
	}
	f.parsed = true
	for _, name := range sortedKeys(doc.Services) {
		s := doc.Services[name]
		svc := Service{Name: name, Image: s.Image, File: f.rel}
		switch s.Build.Kind {
		case yaml.ScalarNode:
			svc.Build = s.Build.Value
		case yaml.MappingNode:
			for i := 0; i+1 < len(s.Build.Content); i += 2 {
				if s.Build.Content[i].Value == "context" {
					svc.Build = s.Build.Content[i+1].Value
				}
			}
			if svc.Build == "" {
				svc.Build = "."
			}
		}
		for _, p := range s.Ports {
			switch v := p.(type) {
			case string:
				svc.Ports = append(svc.Ports, v)
			case int:
				svc.Ports = append(svc.Ports, fmt.Sprint(v))
			case map[string]any:
				pub, tgt := fmt.Sprint(v["published"]), fmt.Sprint(v["target"])
				if v["published"] == nil {
					svc.Ports = append(svc.Ports, tgt)
				} else {
					svc.Ports = append(svc.Ports, pub+":"+tgt)
				}
			}
		}
		switch s.DependsOn.Kind {
		case yaml.SequenceNode:
			for _, n := range s.DependsOn.Content {
				svc.DependsOn = append(svc.DependsOn, n.Value)
			}
		case yaml.MappingNode:
			for i := 0; i < len(s.DependsOn.Content); i += 2 {
				svc.DependsOn = append(svc.DependsOn, s.DependsOn.Content[i].Value)
			}
		}
		svc.DependsOn = uniqSorted(svc.DependsOn)
		switch s.Environment.Kind {
		case yaml.SequenceNode:
			for _, n := range s.Environment.Content {
				k, _, _ := strings.Cut(n.Value, "=")
				a.composeEnv[strings.TrimSpace(k)] = true
			}
		case yaml.MappingNode:
			for i := 0; i < len(s.Environment.Content); i += 2 {
				a.composeEnv[s.Environment.Content[i].Value] = true
			}
		}
		a.services = append(a.services, svc)
	}
}

// dockerfile records the last CMD / ENTRYPOINT.
func (a *analyzer) dockerfile(f *fileInfo) {
	lines := strings.Split(string(f.content), "\n")
	var entry, cmd string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		for strings.HasSuffix(strings.TrimRight(l, " \t\r"), `\`) && i+1 < len(lines) {
			i++
			l = strings.TrimSuffix(strings.TrimRight(l, " \t\r"), `\`) + " " + strings.TrimSpace(lines[i])
		}
		if m := dockerCmdRe.FindStringSubmatch(l); m != nil {
			if strings.EqualFold(m[1], "CMD") {
				cmd = strings.TrimSpace(m[2])
			} else {
				entry, cmd = strings.TrimSpace(m[2]), ""
			}
		}
	}
	var parts []string
	if entry != "" {
		parts = append(parts, "ENTRYPOINT "+entry)
	}
	if cmd != "" {
		parts = append(parts, "CMD "+cmd)
	}
	a.entries = append(a.entries, EntryPoint{Path: f.rel, Kind: "Dockerfile", Detail: clip(strings.Join(parts, " "), 160)})
}

// makefile lists the first dozen targets.
func (a *analyzer) makefile(f *fileInfo) {
	var targets []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(f.content), "\n") {
		if strings.HasPrefix(l, "\t") || strings.HasPrefix(l, " ") {
			continue
		}
		m := makeTgtRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		rest := l[len(m[0]):]
		if strings.HasPrefix(rest, "=") || strings.Contains(m[1], "%") || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		targets = append(targets, m[1])
		if len(targets) >= 12 {
			break
		}
	}
	a.entries = append(a.entries, EntryPoint{Path: f.rel, Kind: "Makefile", Detail: strings.Join(targets, ", ")})
}
