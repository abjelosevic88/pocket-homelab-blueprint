package analyze

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	specYAMLRe = regexp.MustCompile(`(?m)^["']?(openapi|swagger)["']?\s*:`)
	specJSONRe = regexp.MustCompile(`"(openapi|swagger)"\s*:\s*"`)
)

var specMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// looksLikeSpec is a cheap check for a top-level openapi/swagger key.
func looksLikeSpec(f *fileInfo) bool {
	lb := strings.ToLower(f.base)
	isJSON := strings.HasSuffix(lb, ".json")
	if !(strings.HasSuffix(lb, ".yaml") || strings.HasSuffix(lb, ".yml") || isJSON) {
		return false
	}
	head := f.content
	if !bytes.Contains(head, []byte("openapi")) && !bytes.Contains(head, []byte("swagger")) {
		return false
	}
	if isJSON {
		if len(head) > 65536 {
			head = head[:65536]
		}
		return specJSONRe.Match(head)
	}
	if len(head) > 4096 {
		head = head[:4096]
	}
	return specYAMLRe.Match(head)
}

// openapi reads OpenAPI 3 / Swagger 2 documents.
func (a *analyzer) openapi() {
	for _, f := range a.files {
		if f.spec && f.content != nil {
			a.readSpec(f)
		}
	}
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func (a *analyzer) readSpec(f *fileInfo) {
	var doc yaml.Node
	if err := yaml.Unmarshal(f.content, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}
	sf := SpecFile{Path: f.rel}
	base := ""
	if v := scalar(mapGet(root, "openapi")); v != "" {
		sf.Kind = "OpenAPI " + v
		if servers := mapGet(root, "servers"); servers != nil && servers.Kind == yaml.SequenceNode && len(servers.Content) > 0 {
			base = specBasePath(scalar(mapGet(servers.Content[0], "url")))
		}
	} else if v := scalar(mapGet(root, "swagger")); v != "" {
		sf.Kind = "Swagger " + v
		base = specBasePath(scalar(mapGet(root, "basePath")))
	} else {
		return
	}
	f.parsed = true
	info := mapGet(root, "info")
	sf.Title = scalar(mapGet(info, "title"))
	sf.Version = scalar(mapGet(info, "version"))
	paths := mapGet(root, "paths")
	if paths != nil && paths.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(paths.Content); i += 2 {
			key, item := paths.Content[i], paths.Content[i+1]
			for _, m := range specMethods {
				op := mapGet(item, m)
				if op == nil {
					continue
				}
				ep := Endpoint{Method: strings.ToUpper(m), Path: joinPath(base, key.Value), File: f.rel, Line: key.Line,
					Framework: "openapi", Source: "spec", Handler: scalar(mapGet(op, "operationId"))}
				ep.Summary = clip(scalar(mapGet(op, "summary")), 200)
				if ep.Summary == "" {
					ep.Summary = firstSentence(scalar(mapGet(op, "description")))
				}
				if tags := mapGet(op, "tags"); tags != nil && tags.Kind == yaml.SequenceNode && len(tags.Content) > 0 {
					ep.Tag = scalar(tags.Content[0])
				}
				if ep.Line == 0 {
					ep.Line = 1
				}
				f.endpoints = append(f.endpoints, epRaw{Endpoint: ep})
				sf.Endpoints++
			}
		}
	}
	a.specFiles = append(a.specFiles, sf)
}

// specBasePath keeps the path part of a server URL / basePath.
func specBasePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "{") && !strings.HasPrefix(s, "/") {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		s = u.Path
	}
	s = strings.TrimRight(s, "/")
	if s != "" && !strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}
