package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- notes ----

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.GetCodebase(ctx, uid, cid); err != nil {
		s.fail(w, err, "codebase")
		return
	}
	_, hasPath := r.URL.Query()["path"]
	list, err := s.store.ListNotes(ctx, uid, cid, strings.Trim(r.URL.Query().Get("path"), "/"), !hasPath)
	if err != nil {
		s.fail(w, err, "notes")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cid := idParam(r, "id")
	if _, err := s.store.GetCodebase(ctx, uid, cid); err != nil {
		s.fail(w, err, "codebase")
		return
	}
	var in struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "write something first")
		return
	}
	n := &store.Note{CodebaseID: cid, Path: in.Path, Kind: "note", Text: strings.TrimSpace(in.Text)}
	if err := s.store.SaveNote(ctx, uid, n); err != nil {
		s.fail(w, err, "save note")
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "write something first")
		return
	}
	n, err := s.store.UpdateNote(r.Context(), actorID(r.Context()), idParam(r, "id"), strings.TrimSpace(in.Text))
	if err != nil {
		s.fail(w, err, "save note")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteNote(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete note")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- search across every codebase ----

type searchHit struct {
	CodebaseID int64  `json:"codebase_id"`
	Codebase   string `json:"codebase"`
	ScanID     int64  `json:"scan_id"`
	Kind       string `json:"kind"` // file | symbol | endpoint | table | env
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Path       string `json:"path"`
	Line       int    `json:"line,omitempty"`
	rank       int
}

// handleSearch looks for a name in the latest finished scan of each of the
// person's codebases: files, symbols, endpoints, tables and env variables.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, []searchHit{})
		return
	}
	only := idOrZero(r.URL.Query().Get("codebase"))
	list, err := s.store.ListCodebases(ctx, uid)
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	var hits []searchHit
	rank := func(name string) int {
		n := strings.ToLower(name)
		switch {
		case n == q:
			return 0
		case strings.HasPrefix(n, q):
			return 1
		case strings.Contains(n, q):
			return 2
		}
		return -1
	}
	for _, c := range list {
		if c.Done == nil || (only > 0 && c.ID != only) {
			continue
		}
		res, err := s.result(ctx, uid, c.Done.ID)
		if err != nil {
			continue
		}
		add := func(h searchHit, rk int) {
			h.CodebaseID, h.Codebase, h.ScanID, h.rank = c.ID, c.Name, c.Done.ID, rk
			hits = append(hits, h)
		}
		for _, f := range res.Files {
			base := f.Path[strings.LastIndex(f.Path, "/")+1:]
			if rk := rank(base); rk >= 0 {
				add(searchHit{Kind: "file", Title: base, Detail: f.Path, Path: f.Path}, rk+1)
			}
		}
		for _, sy := range res.Symbols {
			if rk := rank(sy.Name); rk >= 0 {
				t := sy.Name
				if sy.Parent != "" {
					t = sy.Parent + "." + sy.Name
				}
				if !sy.Exported {
					rk += 2
				}
				add(searchHit{Kind: "symbol", Title: t, Detail: sy.Kind + " in " + sy.File, Path: sy.File, Line: sy.Line}, rk)
			}
		}
		for _, e := range res.Endpoints {
			if rk := rank(e.Path); rk >= 0 || rank(e.Handler) >= 0 {
				if rk < 0 {
					rk = 2
				}
				add(searchHit{Kind: "endpoint", Title: e.Method + " " + e.Path, Detail: e.Handler, Path: e.File, Line: e.Line}, rk)
			}
		}
		for _, t := range res.Tables {
			if rk := rank(t.Name); rk >= 0 {
				add(searchHit{Kind: "table", Title: t.Name, Detail: "table in " + t.File, Path: t.File, Line: t.Line}, rk)
			}
		}
		for _, v := range res.EnvVars {
			if rk := rank(v.Name); rk >= 0 {
				add(searchHit{Kind: "env", Title: v.Name, Detail: "environment variable", Path: firstUse(v.Uses)}, rk+1)
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].Title < hits[j].Title
	})
	writeJSON(w, http.StatusOK, topN(nonNil(hits), limit))
}

func firstUse(uses []string) string {
	if len(uses) == 0 {
		return ""
	}
	u := uses[0]
	if i := strings.LastIndex(u, ":"); i > 0 {
		return u[:i]
	}
	return u
}

func idOrZero(v string) int64 {
	var n int64
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}
