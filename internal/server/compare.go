package server

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/analyze"
)

// ---- comparing two scans ----

type fileChange struct {
	Path   string `json:"path"`
	Before int    `json:"before"` // lines
	After  int    `json:"after"`
}

type tableChange struct {
	Name    string   `json:"name"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

type extChange struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func epKey(e analyze.Endpoint) string { return e.Method + " " + e.Path }

// handleCompare lists what changed from scan {other} (before) to scan {id} (after).
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	scB, b, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	scA, err := s.store.GetScan(ctx, uid, idParam(r, "other"))
	if err != nil || scA.CodebaseID != scB.CodebaseID {
		writeErr(w, http.StatusNotFound, "pick another scan of the same codebase")
		return
	}
	a, err := s.result(ctx, uid, scA.ID)
	if err != nil {
		s.fail(w, err, "scan result")
		return
	}
	// files
	fa := map[string]analyze.File{}
	for _, f := range a.Files {
		fa[f.Path] = f
	}
	added, removed, changed := []string{}, []string{}, []fileChange{}
	seen := map[string]bool{}
	for _, f := range b.Files {
		seen[f.Path] = true
		old, ok := fa[f.Path]
		if !ok {
			added = append(added, f.Path)
			continue
		}
		if old.Lines != f.Lines || old.Bytes != f.Bytes {
			changed = append(changed, fileChange{f.Path, old.Lines, f.Lines})
		}
	}
	for _, f := range a.Files {
		if !seen[f.Path] {
			removed = append(removed, f.Path)
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		di, dj := abs(changed[i].After-changed[i].Before), abs(changed[j].After-changed[j].Before)
		if di != dj {
			return di > dj
		}
		return changed[i].Path < changed[j].Path
	})
	// endpoints
	ea := map[string]analyze.Endpoint{}
	for _, e := range a.Endpoints {
		ea[epKey(e)] = e
	}
	eb := map[string]analyze.Endpoint{}
	for _, e := range b.Endpoints {
		eb[epKey(e)] = e
	}
	epAdded, epRemoved := []analyze.Endpoint{}, []analyze.Endpoint{}
	for k, e := range eb {
		if _, ok := ea[k]; !ok {
			epAdded = append(epAdded, e)
		}
	}
	for k, e := range ea {
		if _, ok := eb[k]; !ok {
			epRemoved = append(epRemoved, e)
		}
	}
	byPath := func(l []analyze.Endpoint) {
		sort.Slice(l, func(i, j int) bool { return epKey(l[i]) < epKey(l[j]) })
	}
	byPath(epAdded)
	byPath(epRemoved)
	// tables
	ta := map[string]analyze.Table{}
	for _, t := range a.Tables {
		ta[t.Name] = t
	}
	tAdded, tRemoved, tChanged := []string{}, []string{}, []tableChange{}
	tb := map[string]bool{}
	for _, t := range b.Tables {
		tb[t.Name] = true
		old, ok := ta[t.Name]
		if !ok {
			tAdded = append(tAdded, t.Name)
			continue
		}
		ca := map[string]string{}
		for _, c := range old.Columns {
			ca[c.Name] = c.Type
		}
		cb := map[string]bool{}
		ch := tableChange{Name: t.Name, Added: []string{}, Removed: []string{}}
		for _, c := range t.Columns {
			cb[c.Name] = true
			if _, ok := ca[c.Name]; !ok {
				ch.Added = append(ch.Added, c.Name)
			}
		}
		for _, c := range old.Columns {
			if !cb[c.Name] {
				ch.Removed = append(ch.Removed, c.Name)
			}
		}
		if len(ch.Added)+len(ch.Removed) > 0 {
			tChanged = append(tChanged, ch)
		}
	}
	for _, t := range a.Tables {
		if !tb[t.Name] {
			tRemoved = append(tRemoved, t.Name)
		}
	}
	// outside packages
	xa := map[string]analyze.External{}
	for _, x := range a.Externals {
		xa[x.Ecosystem+":"+x.Name] = x
	}
	xAdded, xRemoved, xChanged := []analyze.External{}, []analyze.External{}, []extChange{}
	xb := map[string]bool{}
	for _, x := range b.Externals {
		k := x.Ecosystem + ":" + x.Name
		xb[k] = true
		old, ok := xa[k]
		switch {
		case !ok:
			xAdded = append(xAdded, x)
		case old.Version != x.Version:
			xChanged = append(xChanged, extChange{x.Name, x.Ecosystem, old.Version, x.Version})
		}
	}
	for k, x := range xa {
		if !xb[k] {
			xRemoved = append(xRemoved, x)
		}
	}
	sort.Slice(xRemoved, func(i, j int) bool { return xRemoved[i].Name < xRemoved[j].Name })
	// environment
	va := map[string]bool{}
	for _, v := range a.EnvVars {
		if len(v.Uses) > 0 {
			va[v.Name] = true
		}
	}
	vAdded, vRemoved := []string{}, []string{}
	vb := map[string]bool{}
	for _, v := range b.EnvVars {
		if len(v.Uses) == 0 {
			continue
		}
		vb[v.Name] = true
		if !va[v.Name] {
			vAdded = append(vAdded, v.Name)
		}
	}
	for n := range va {
		if !vb[n] {
			vRemoved = append(vRemoved, n)
		}
	}
	sort.Strings(vRemoved)
	writeJSON(w, http.StatusOK, map[string]any{
		"before": scA, "after": scB, "stats": map[string]any{"before": a.Stats, "after": b.Stats},
		"files":     map[string]any{"added": added, "removed": removed, "changed": topN(changed, 300), "changed_total": len(changed)},
		"endpoints": map[string]any{"added": epAdded, "removed": epRemoved},
		"tables":    map[string]any{"added": tAdded, "removed": tRemoved, "changed": tChanged},
		"externals": map[string]any{"added": xAdded, "removed": xRemoved, "changed": xChanged},
		"env":       map[string]any{"added": vAdded, "removed": vRemoved},
		"cycles":    map[string]any{"before": len(a.Cycles), "after": len(b.Cycles)},
	})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ---- a Markdown report of one scan ----

func (s *Server) handleReportMD(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if queryBool(r, "download") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeName(res.Name)+`-blueprint.md"`)
	}
	_, _ = w.Write([]byte(reportMD(res, sc.FinishedAt, sc.Commit)))
}

func mdEsc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func reportMD(res *analyze.Result, when, commit string) string {
	var b strings.Builder
	st := res.Stats
	fmt.Fprintf(&b, "# %s — blueprint\n\n", res.Name)
	fmt.Fprintf(&b, "Scanned %s", strings.Replace(strings.SplitN(when, ".", 2)[0], "T", " ", 1))
	if commit != "" {
		fmt.Fprintf(&b, " at commit `%s`", commit)
	}
	if res.Git != nil && res.Git.Branch != "" {
		fmt.Fprintf(&b, " on `%s`", res.Git.Branch)
	}
	fmt.Fprintf(&b, ".\n\n%d files (%d code), %s lines, %d folders, %d symbols, %d imports between files, %d endpoints, %d tables, %d environment variables, %d outside packages.\n\n",
		st.Files, st.CodeFiles, groupDigits(st.Lines), st.Dirs, st.Symbols, st.Edges, st.Endpoints, st.Tables, st.EnvVars, st.Externals)
	if len(res.Languages) > 0 {
		b.WriteString("## Languages\n\n| Language | Files | Lines |\n|---|---:|---:|\n")
		for _, l := range topN(res.Languages, 12) {
			fmt.Fprintf(&b, "| %s | %d | %d |\n", l.Lang, l.Files, l.Lines)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Structure\n\n| Folder | Files | Lines |\n|---|---:|---:|\n")
	for _, d := range topN(topDirs(res, ""), 25) {
		fmt.Fprintf(&b, "| `%s` | %d | %d |\n", d.Path, d.Files, d.Lines)
	}
	b.WriteString("\n")
	if len(res.EntryPoints) > 0 {
		b.WriteString("## Entry points\n\n")
		for _, e := range res.EntryPoints {
			fmt.Fprintf(&b, "- **%s** `%s`", e.Kind, e.Path)
			if e.Detail != "" {
				fmt.Fprintf(&b, " — %s", e.Detail)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(res.Endpoints) > 0 {
		b.WriteString("## API endpoints\n\n| Method | Path | Handler | Where |\n|---|---|---|---|\n")
		for _, e := range topN(res.Endpoints, 400) {
			fmt.Fprintf(&b, "| %s | `%s` | %s | `%s:%d` |\n", e.Method, mdEsc(e.Path), mdEsc(e.Handler), e.File, e.Line)
		}
		if len(res.Endpoints) > 400 {
			fmt.Fprintf(&b, "\n…and %d more.\n", len(res.Endpoints)-400)
		}
		b.WriteString("\n")
	}
	if len(res.Tables) > 0 {
		b.WriteString("## Database tables\n\n")
		for _, t := range res.Tables {
			cols := make([]string, 0, len(t.Columns))
			for _, c := range t.Columns {
				x := c.Name
				if c.PK {
					x += " (key)"
				}
				if c.Ref != "" {
					x += " → " + c.Ref
				}
				cols = append(cols, x)
			}
			fmt.Fprintf(&b, "- **%s** (`%s`): %s\n", t.Name, t.File, strings.Join(cols, ", "))
		}
		b.WriteString("\n")
	}
	if len(res.EnvVars) > 0 {
		b.WriteString("## Environment variables\n\n| Name | Read in | Documented |\n|---|---|---|\n")
		for _, v := range res.EnvVars {
			where := "—"
			if len(v.Uses) > 0 {
				where = "`" + v.Uses[0] + "`"
				if len(v.Uses) > 1 {
					where += fmt.Sprintf(" +%d", len(v.Uses)-1)
				}
			}
			doc := "no"
			if v.Documented {
				doc = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", v.Name, where, doc)
		}
		b.WriteString("\n")
	}
	if len(res.Services) > 0 {
		b.WriteString("## Services\n\n")
		for _, sv := range res.Services {
			fmt.Fprintf(&b, "- **%s**", sv.Name)
			if sv.Image != "" {
				fmt.Fprintf(&b, " image `%s`", sv.Image)
			}
			if sv.Build != "" {
				fmt.Fprintf(&b, " built from `%s`", sv.Build)
			}
			if len(sv.Ports) > 0 {
				fmt.Fprintf(&b, ", ports %s", strings.Join(sv.Ports, ", "))
			}
			if len(sv.DependsOn) > 0 {
				fmt.Fprintf(&b, ", needs %s", strings.Join(sv.DependsOn, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(res.Externals) > 0 {
		b.WriteString("## Outside packages\n\n")
		ext := append([]analyze.External(nil), res.Externals...)
		sort.SliceStable(ext, func(i, j int) bool { return ext[i].Files > ext[j].Files })
		for _, x := range topN(ext, 60) {
			fmt.Fprintf(&b, "- `%s`", x.Name)
			if x.Version != "" {
				fmt.Fprintf(&b, " %s", x.Version)
			}
			fmt.Fprintf(&b, " (%s, used in %d files)\n", x.Ecosystem, x.Files)
		}
		b.WriteString("\n")
	}
	if hs := topN(hotspots(res), 15); len(hs) > 0 && res.Git != nil {
		b.WriteString("## Hotspots (big files that change often)\n\n| File | Lines | Commits | Score |\n|---|---:|---:|---:|\n")
		for _, h := range hs {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %.0f |\n", h.Path, h.Lines, h.Churn, h.Score)
		}
		b.WriteString("\n")
	}
	if len(res.Cycles) > 0 {
		b.WriteString("## Import loops between folders\n\n")
		for _, c := range res.Cycles {
			fmt.Fprintf(&b, "- %s\n", "`"+strings.Join(c, "` ↔ `")+"`")
		}
		b.WriteString("\n")
	}
	if len(res.Warnings) > 0 {
		b.WriteString("## Notes from the scan\n\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}
