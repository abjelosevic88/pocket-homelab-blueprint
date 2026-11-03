package server

import (
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/analyze"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- reading a scan ----

// scanFor loads the scan in the URL with its result (the person's own, or
// of a codebase shared with them).
func (s *Server) scanFor(w http.ResponseWriter, r *http.Request) (*store.Scan, *analyze.Result, bool) {
	ctx := r.Context()
	sc, c, err := s.scanAccess(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "scan")
		return nil, nil, false
	}
	if sc.Status != "done" {
		writeErr(w, http.StatusConflict, "this scan has not finished")
		return nil, nil, false
	}
	res, err := s.result(ctx, c.UserID, sc.ID)
	if err != nil {
		s.fail(w, err, "scan result")
		return nil, nil, false
	}
	return sc, res, true
}

func (s *Server) handleGetScan(w http.ResponseWriter, r *http.Request) {
	sc, _, err := s.scanAccess(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "scan")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

// isTop reports whether p is a direct child of root ("" = top level).
func under(p, root string) bool {
	return root == "" || p == root || strings.HasPrefix(p, root+"/")
}

func topN[T any](list []T, n int) []T {
	if len(list) > n {
		return list[:n]
	}
	return list
}

type hotspot struct {
	Path       string  `json:"path"`
	Lang       string  `json:"lang"`
	Lines      int     `json:"lines"`
	Churn      int     `json:"churn"`
	Authors    int     `json:"authors"`
	ImportedBy int     `json:"imported_by"`
	Todos      int     `json:"todos"`
	LastChange string  `json:"last_change"`
	Score      float64 `json:"score"` // 0..100
}

// hotspots ranks code files by how often they change × how big they are:
// big files that change all the time are where bugs and slow reviews live.
func hotspots(res *analyze.Result) []hotspot {
	var out []hotspot
	maxRaw := 0.0
	for _, f := range res.Files {
		if f.Lang == "" || f.Generated || f.Lines == 0 {
			continue
		}
		raw := float64(f.Churn) * math.Log2(float64(f.Code)+2)
		if res.Git == nil {
			raw = math.Log2(float64(f.Code)+2) * (1 + float64(f.ImportedBy)/4)
		}
		if raw > maxRaw {
			maxRaw = raw
		}
		out = append(out, hotspot{Path: f.Path, Lang: f.Lang, Lines: f.Lines, Churn: f.Churn, Authors: f.Authors, ImportedBy: f.ImportedBy, Todos: f.Todos, LastChange: f.LastChange, Score: raw})
	}
	for i := range out {
		if maxRaw > 0 {
			out[i].Score = math.Round(out[i].Score/maxRaw*1000) / 10
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Path < out[j].Path
	})
	return out
}

type dirSum struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Lines int    `json:"lines"`
	Lang  string `json:"lang"`
}

// topDirs are the folders right below root with their files added up.
func topDirs(res *analyze.Result, root string) []dirSum {
	sums := map[string]*dirSum{}
	for _, f := range res.Files {
		if !under(f.Path, root) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(f.Path, root), "/")
		key := root
		if i := strings.Index(rel, "/"); i > 0 {
			key = path.Join(root, rel[:i])
		} else {
			key = path.Join(root, "(files here)")
			if root == "" {
				key = "(top-level files)"
			}
		}
		d := sums[key]
		if d == nil {
			d = &dirSum{Path: key}
			sums[key] = d
		}
		d.Files++
		d.Lines += f.Lines
	}
	langs := map[string]string{}
	for _, d := range res.Dirs {
		langs[d.Path] = d.Lang
	}
	out := make([]dirSum, 0, len(sums))
	for _, d := range sums {
		d.Lang = langs[d.Path]
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lines > out[j].Lines })
	return out
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	methods := map[string]int{}
	frameworks := map[string]int{}
	for _, e := range res.Endpoints {
		methods[e.Method]++
		frameworks[e.Framework]++
	}
	ext := append([]analyze.External(nil), res.Externals...)
	sort.SliceStable(ext, func(i, j int) bool { return ext[i].Files > ext[j].Files })
	largest := make([]analyze.File, 0, len(res.Files))
	for _, f := range res.Files {
		if f.Lang != "" && !f.Generated {
			largest = append(largest, f)
		}
	}
	sort.SliceStable(largest, func(i, j int) bool { return largest[i].Lines > largest[j].Lines })
	central := append([]analyze.File(nil), largest...)
	sort.SliceStable(central, func(i, j int) bool { return central[i].ImportedBy > central[j].ImportedBy })
	for len(central) > 0 && central[len(central)-1].ImportedBy == 0 {
		central = central[:len(central)-1]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scan": sc, "name": res.Name, "stats": res.Stats, "languages": res.Languages, "entry_points": res.EntryPoints,
		"externals": topN(ext, 24), "externals_total": len(res.Externals), "warnings": res.Warnings, "git": res.Git, "cycles": res.Cycles,
		"top_dirs": topN(topDirs(res, ""), 16), "hotspots": topN(hotspots(res), 8), "largest": topN(largest, 8), "central": topN(central, 8),
		"methods": methods, "frameworks": frameworks, "spec_files": res.SpecFiles, "services": res.Services,
	})
}

// handleTree returns every file and folder (the Structure page builds the tree).
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": res.Files, "dirs": res.Dirs})
}

type link struct {
	Path  string   `json:"path"`
	Names []string `json:"names,omitempty"`
	Count int      `json:"count,omitempty"`
}

// handleFile describes one file or folder: what is in it, what it uses and
// what uses it.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	p := strings.Trim(r.URL.Query().Get("path"), "/")
	notes, _ := s.store.ListNotes(r.Context(), actorID(r.Context()), sc.CodebaseID, p, false)
	for _, f := range res.Files {
		if f.Path != p {
			continue
		}
		var syms []analyze.Symbol
		for _, sy := range res.Symbols {
			if sy.File == p {
				syms = append(syms, sy)
			}
		}
		var imports, importedBy []link
		for _, e := range res.Edges {
			if e.From == p {
				imports = append(imports, link{Path: e.To, Names: e.Names})
			}
			if e.To == p {
				importedBy = append(importedBy, link{Path: e.From, Names: e.Names})
			}
		}
		var eps []analyze.Endpoint
		for _, e := range res.Endpoints {
			if e.File == p {
				eps = append(eps, e)
			}
		}
		var env []map[string]any
		for _, v := range res.EnvVars {
			for _, u := range v.Uses {
				if strings.HasPrefix(u, p+":") {
					env = append(env, map[string]any{"name": v.Name, "use": u, "documented": v.Documented})
				}
			}
		}
		var calls []analyze.Call
		for _, c := range res.Calls {
			if strings.HasPrefix(c.From, p+"#") || strings.HasPrefix(c.To, p+"#") {
				calls = append(calls, c)
			}
		}
		var tables []analyze.Table
		for _, t := range res.Tables {
			if t.File == p {
				tables = append(tables, t)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"kind": "file", "file": f, "symbols": nonNil(syms), "imports": nonNil(imports), "imported_by": nonNil(importedBy),
			"endpoints": nonNil(eps), "env": nonNil(env), "calls": nonNil(topN(calls, 400)), "tables": nonNil(tables), "notes": notes})
		return
	}
	var dir *analyze.Dir
	for i := range res.Dirs {
		if res.Dirs[i].Path == p {
			dir = &res.Dirs[i]
			break
		}
	}
	if dir == nil {
		writeErr(w, http.StatusNotFound, "not in this scan")
		return
	}
	var files []analyze.File
	subdirs := []analyze.Dir{}
	for _, f := range res.Files {
		if f.Dir == p {
			files = append(files, f)
		}
	}
	for _, d := range res.Dirs {
		if d.Path != p && path.Dir(d.Path) == p || (p == "" && d.Path != "" && !strings.Contains(d.Path, "/")) {
			subdirs = append(subdirs, d)
		}
	}
	// the folder's links to other folders, counted in file imports
	out, in := map[string]int{}, map[string]int{}
	for _, e := range res.Edges {
		fi, ti := under(e.From, p), under(e.To, p)
		switch {
		case fi && !ti:
			out[topOf(e.To, p)]++
		case ti && !fi:
			in[topOf(e.From, p)]++
		}
	}
	var eps []analyze.Endpoint
	syms := 0
	for _, e := range res.Endpoints {
		if under(e.File, p) {
			eps = append(eps, e)
		}
	}
	for _, sy := range res.Symbols {
		if under(sy.File, p) {
			syms++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "dir", "dir": dir, "files": nonNil(files), "subdirs": subdirs, "depends_on": sortedLinks(out), "used_by": sortedLinks(in),
		"endpoints": nonNil(topN(eps, 200)), "endpoints_total": len(eps), "symbols": syms, "notes": notes})
}

// topOf names the other end of a folder link: its folder.
func topOf(p, _ string) string {
	return path.Dir(p)
}

func sortedLinks(m map[string]int) []link {
	out := make([]link, 0, len(m))
	for k, v := range m {
		if k == "." {
			k = ""
		}
		out = append(out, link{Path: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// codeRoot is where a codebase's code is on disk now (without fetching it).
func (s *Server) codeRoot(c *store.Codebase) string {
	var root string
	switch c.Source {
	case "path":
		root = c.Location
	case "git":
		root = filepath.Join(s.workDir(c.ID), "repo")
	default:
		root = filepath.Join(s.workDir(c.ID), "src")
	}
	if sub, err := cleanSubdir(c.Subdir); err == nil && sub != "" {
		root = filepath.Join(root, filepath.FromSlash(sub))
	}
	return root
}

// handleSource returns a file's text as it is on disk now.
func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := s.codebaseFor(ctx, sc.CodebaseID)
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	if c.Source == "path" && userFrom(ctx).Role != "admin" {
		writeErr(w, http.StatusForbidden, "only admins can read folders on the server")
		return
	}
	p := strings.Trim(r.URL.Query().Get("path"), "/")
	var file *analyze.File
	for i := range res.Files {
		if res.Files[i].Path == p {
			file = &res.Files[i]
			break
		}
	}
	if file == nil {
		writeErr(w, http.StatusNotFound, "not in this scan")
		return
	}
	root, err := filepath.EvalSymlinks(s.codeRoot(c))
	if err != nil {
		writeErr(w, http.StatusNotFound, "the code is not on this server any more; scan again")
		return
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil || !strings.HasPrefix(full, root+string(filepath.Separator)) {
		writeErr(w, http.StatusNotFound, "the file is not there any more")
		return
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "the file is not there any more")
		return
	}
	const max = 2 << 20
	if fi.Size() > max {
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "too_big": true, "bytes": fi.Size()})
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		s.fail(w, err, "read")
		return
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b[:min(len(b), 8000)]), 0) {
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "binary": true, "bytes": fi.Size()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "lang": file.Lang, "text": string(b), "changed": fi.Size() != file.Bytes, "bytes": fi.Size()})
}

// ---- the graph ----

type gNode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   string `json:"kind"` // dir | file
	Parent string `json:"parent,omitempty"`
	Lang   string `json:"lang,omitempty"`
	Files  int    `json:"files"`
	Lines  int    `json:"lines"`
	In     int    `json:"in"`
	Out    int    `json:"out"`
	Cycle  bool   `json:"cycle,omitempty"`
	Test   bool   `json:"test,omitempty"`
}

type gEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
	Cycle  bool   `json:"cycle,omitempty"`
}

// graphLang leaves out what never imports anything: docs, data and build files.
func graphLang(l string) bool {
	switch l {
	case "", "Markdown", "JSON", "YAML", "TOML", "XML", "CSV", "Text", "Dockerfile", "Makefile", "SQL", "INI", "Shell", "HTML", "CSS", "SCSS":
		return false
	}
	return true
}

// handleGraph rolls the file imports up to a graph: folders down to a depth
// below root, or single files. focus+radius keep only what is near one node.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	level := q.Get("level")
	root := strings.Trim(q.Get("root"), "/")
	depth := queryInt(r, "depth")
	if depth <= 0 {
		depth = 1
	}
	tests := queryBool(r, "tests")
	focus := strings.Trim(q.Get("focus"), "/")
	radius := queryInt(r, "radius")
	if radius <= 0 {
		radius = 1
	}
	fileOf := map[string]*analyze.File{}
	for i := range res.Files {
		fileOf[res.Files[i].Path] = &res.Files[i]
	}
	keep := func(f *analyze.File) bool {
		return f != nil && graphLang(f.Lang) && under(f.Path, root) && (tests || !f.Test) && !f.Generated
	}
	key := func(f *analyze.File) string {
		if level == "file" {
			return f.Path
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(f.Dir, root), "/")
		if rel == "" {
			return f.Dir
		}
		parts := strings.Split(rel, "/")
		if len(parts) > depth {
			parts = parts[:depth]
		}
		return strings.Trim(path.Join(root, strings.Join(parts, "/")), "/")
	}
	cycleDir := map[string]bool{}
	for _, c := range res.Cycles {
		for _, d := range c {
			cycleDir[d] = true
		}
	}
	nodes := map[string]*gNode{}
	for i := range res.Files {
		f := &res.Files[i]
		if !keep(f) {
			continue
		}
		k := key(f)
		n := nodes[k]
		if n == nil {
			n = &gNode{ID: k, Kind: "dir", Label: path.Base(k), Lang: f.Lang}
			if k == "" {
				n.Label = res.Name
			}
			if level == "file" {
				n.Kind, n.Parent, n.Test = "file", f.Dir, f.Test
			}
			nodes[k] = n
		}
		n.Files++
		n.Lines += f.Lines
		if cycleDir[f.Dir] {
			n.Cycle = true
		}
	}
	if level != "file" {
		langs := map[string]string{}
		for _, d := range res.Dirs {
			langs[d.Path] = d.Lang
		}
		for k, n := range nodes {
			if l := langs[k]; l != "" {
				n.Lang = l
			}
		}
	}
	weights := map[[2]string]int{}
	for _, e := range res.Edges {
		a, b := fileOf[e.From], fileOf[e.To]
		if !keep(a) || !keep(b) {
			continue
		}
		ka, kb := key(a), key(b)
		if ka == kb {
			continue
		}
		weights[[2]string{ka, kb}]++
	}
	// focus: breadth-first both ways from the focused node
	if focus != "" {
		start := focus
		if _, ok := nodes[start]; !ok {
			if f := fileOf[focus]; f != nil && keep(f) {
				start = key(f)
			}
		}
		adj := map[string][]string{}
		for k := range weights {
			adj[k[0]] = append(adj[k[0]], k[1])
			adj[k[1]] = append(adj[k[1]], k[0])
		}
		seen := map[string]bool{start: true}
		frontier := []string{start}
		for d := 0; d < radius; d++ {
			var next []string
			for _, n := range frontier {
				for _, m := range adj[n] {
					if !seen[m] {
						seen[m] = true
						next = append(next, m)
					}
				}
			}
			frontier = next
		}
		for k := range nodes {
			if !seen[k] {
				delete(nodes, k)
			}
		}
	}
	edges := []gEdge{}
	for k, wgt := range weights {
		na, nb := nodes[k[0]], nodes[k[1]]
		if na == nil || nb == nil {
			continue
		}
		_, back := weights[[2]string{k[1], k[0]}]
		edges = append(edges, gEdge{From: k[0], To: k[1], Weight: wgt, Cycle: back || (na.Cycle && nb.Cycle && level != "file")})
		na.Out++
		nb.In++
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	list := make([]*gNode, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	const maxNodes = 900
	truncated := false
	if len(list) > maxNodes {
		truncated = true
		sort.SliceStable(list, func(i, j int) bool { return list[i].In+list[i].Out > list[j].In+list[j].Out })
		list = list[:maxNodes]
		in := map[string]bool{}
		for _, n := range list {
			in[n.ID] = true
		}
		kept := edges[:0]
		for _, e := range edges {
			if in[e.From] && in[e.To] {
				kept = append(kept, e)
			}
		}
		edges = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": list, "edges": edges, "truncated": truncated, "level": level, "root": root, "depth": depth})
}

// ---- symbols ----

func (s *Server) handleSymbols(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	kind := r.URL.Query().Get("kind")
	root := strings.Trim(r.URL.Query().Get("root"), "/")
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	type hit struct {
		analyze.Symbol
		rank int
	}
	var hits []hit
	for _, sy := range res.Symbols {
		if kind != "" && sy.Kind != kind || !under(sy.File, root) {
			continue
		}
		name := strings.ToLower(sy.Name)
		full := strings.ToLower(sy.Parent + "." + sy.Name)
		rank := 0
		switch {
		case q == "":
			rank = 3
		case name == q || full == q:
			rank = 0
		case strings.HasPrefix(name, q):
			rank = 1
		case strings.Contains(name, q) || strings.Contains(full, q):
			rank = 2
		default:
			continue
		}
		if !sy.Exported {
			rank += 4
		}
		hits = append(hits, hit{sy, rank})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if hits[i].Refs != hits[j].Refs {
			return hits[i].Refs > hits[j].Refs
		}
		return hits[i].Name < hits[j].Name
	})
	out := make([]analyze.Symbol, 0, min(limit, len(hits)))
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].Symbol)
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbols": out, "total": len(hits)})
}

// symbolID is how calls name a symbol: "file#Name" or "file#Type.Method".
func symbolID(sy analyze.Symbol) string {
	if sy.Parent != "" && (sy.Kind == "method" || sy.Kind == "func") {
		return sy.File + "#" + sy.Parent + "." + sy.Name
	}
	return sy.File + "#" + sy.Name
}

// handleSymbol describes one symbol with what calls it and what it calls.
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	file, name, parent := r.URL.Query().Get("file"), r.URL.Query().Get("name"), r.URL.Query().Get("parent")
	var sym *analyze.Symbol
	for i := range res.Symbols {
		sy := &res.Symbols[i]
		if sy.File == file && sy.Name == name && (parent == "" || sy.Parent == parent) {
			sym = sy
			break
		}
	}
	if sym == nil {
		writeErr(w, http.StatusNotFound, "not in this scan")
		return
	}
	id := symbolID(*sym)
	callers, callees := []analyze.Call{}, []analyze.Call{}
	for _, c := range res.Calls {
		if c.To == id {
			callers = append(callers, c)
		}
		if c.From == id {
			callees = append(callees, c)
		}
	}
	var users []string
	for _, e := range res.Edges {
		if e.To == sym.File {
			for _, n := range e.Names {
				if n == sym.Name {
					users = append(users, e.From)
					break
				}
			}
		}
	}
	var eps []analyze.Endpoint
	for _, e := range res.Endpoints {
		h := e.Handler
		if i := strings.LastIndex(h, "."); i >= 0 {
			h = h[i+1:]
		}
		if h == sym.Name {
			eps = append(eps, e)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbol": sym, "id": id, "callers": callers, "callees": callees, "imported_by": nonNil(users), "endpoints": nonNil(eps)})
}

// ---- endpoints, data, config, hotspots ----

func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": res.Endpoints, "spec_files": res.SpecFiles})
}

type relation struct {
	From string `json:"from"` // table.column
	To   string `json:"to"`
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	rels := []relation{}
	for _, t := range res.Tables {
		for _, c := range t.Columns {
			if c.Ref != "" {
				rels = append(rels, relation{From: t.Name + "." + c.Name, To: c.Ref})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": res.Tables, "relations": rels})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env_vars": res.EnvVars, "services": res.Services, "entry_points": res.EntryPoints, "externals": res.Externals, "spec_files": res.SpecFiles})
}

func (s *Server) handleHotspots(w http.ResponseWriter, r *http.Request) {
	_, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hotspots": topN(hotspots(res), 300), "git": res.Git != nil, "authors": authorsOf(res)})
}

func authorsOf(res *analyze.Result) []analyze.Author {
	if res.Git == nil {
		return []analyze.Author{}
	}
	return res.Git.Authors
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeName(res.Name)+"-scan-"+itoa(sc.ID)+`.json"`)
	writeJSON(w, http.StatusOK, res)
}

func safeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "codebase"
	}
	return b.String()
}
