package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"keep_scans": st.KeepScans, "vendored": st.Vendored, "git_commits": st.GitCommits, "hide_tests": st.HideTests,
		"graph_layout": st.GraphLayout, "show_external": st.ShowExternal,
		"notify_done": st.NotifyDone, "notify_failed": st.NotifyFailed,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
	for _, a := range pocketApps {
		c := st.Conn(a.slug)
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		out[a.slug+"_url"], out[a.slug+"_token"], out[a.slug+"_public_url"] = c.URL, mask(c.Token), c.PublicURL
		public := c.PublicURL
		if public == "" && via != "" {
			public = s.resolveSibling(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var raw map[string]json.RawMessage
	var in store.Settings
	if json.Unmarshal(body, &raw) != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	keys := map[string]bool{}
	for k := range raw {
		keys[k] = true
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	intIn := func(k string, dst *int, v, lo, hi int) bool {
		if !keys[k] {
			return true
		}
		if v < lo || v > hi {
			bad(fmt.Sprintf("%s is %d to %d", k, lo, hi))
			return false
		}
		*dst = v
		return true
	}
	if !intIn("keep_scans", &cur.KeepScans, in.KeepScans, 1, 100) || !intIn("git_commits", &cur.GitCommits, in.GitCommits, 0, 20000) {
		return
	}
	if keys["graph_layout"] {
		if in.GraphLayout != "layers" && in.GraphLayout != "organic" {
			bad("the graph layout is layers or organic")
			return
		}
		cur.GraphLayout = in.GraphLayout
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	urlIn := func(v string) string { return strings.TrimRight(strings.TrimSpace(v), "/") }
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boolIn("vendored", &cur.Vendored, in.Vendored)
	boolIn("hide_tests", &cur.HideTests, in.HideTests)
	boolIn("show_external", &cur.ShowExternal, in.ShowExternal)
	boolIn("notify_done", &cur.NotifyDone, in.NotifyDone)
	boolIn("notify_failed", &cur.NotifyFailed, in.NotifyFailed)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	if keys["openai_base_url"] {
		cur.OpenAIBaseURL = urlIn(in.OpenAIBaseURL)
	}
	// per app: <slug>_mode, _url, _token, _public_url
	for _, a := range pocketApps {
		c := cur.Conn(a.slug)
		str := func(k string) (string, bool) {
			v, ok := raw[a.slug+"_"+k]
			if !ok {
				return "", false
			}
			var x string
			_ = json.Unmarshal(v, &x)
			return x, true
		}
		if v, ok := str("mode"); ok && (v == modeCore || v == modeDirect || v == modeOff) {
			c.Mode = v
		}
		if v, ok := str("url"); ok {
			c.URL = urlIn(v)
		}
		if v, ok := str("token"); ok && !strings.Contains(v, "••") {
			c.Token = strings.TrimSpace(v)
		}
		if v, ok := str("public_url"); ok {
			c.PublicURL = urlIn(v)
		}
		cur.SetConn(a.slug, c)
	}
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for k := range keys {
		if modeKey(k) {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes()...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				var spaces []map[string]any
				if err = conn.client.Do(ctx, http.MethodGet, "/api/v1/spaces", nil, nil, &spaces); err == nil {
					msg = fmt.Sprintf("%s %s, %s, %d places to pick folders from", a.name, v, how, len(spaces))
				}
			}
		}
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Blueprint", Body: "Notifications work. Finished and failed scans will arrive here.", Priority: "info", Tags: []string{"mag"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Blueprint", Body: "Notifications work.", Priority: "info", Tags: []string{"mag"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "scanned": s.scanned.Load(),
		"source_roots": s.rootsFor(userFrom(r.Context())), "work_bytes": dirSize(filepath.Join(s.cfg.DataDir, "work")), "max_upload_mb": s.cfg.MaxUploadMB, "max_files": s.cfg.MaxFiles, "scan_timeout": s.cfg.ScanTimeout.String(),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-blueprint-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's codebases (without git
// tokens or scan results: those are made again by scanning) and notes.
type exportDoc struct {
	App       string           `json:"app"`
	Version   string           `json:"version"`
	Exported  string           `json:"exported_at"`
	Codebases []exportCodebase `json:"codebases"`
}

type exportCodebase struct {
	store.Codebase
	Notes []store.Note `json:"notes"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-blueprint", Version: Version, Exported: store.Now(), Codebases: []exportCodebase{}}
	list, err := s.store.ListCodebases(ctx, uid)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	for _, c := range list {
		c.Latest, c.Done, c.LastScanID = nil, nil, nil
		notes, _ := s.store.ListNotes(ctx, uid, c.ID, "", true)
		doc.Codebases = append(doc.Codebases, exportCodebase{Codebase: c, Notes: notes})
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-blueprint-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleImportJSON adds the codebases of a Pocket Blueprint export to the
// caller's own (a codebase with the same source and location is kept, its
// notes not repeated). Uploaded codebases come back empty until their
// archive is uploaded again; git codebases need their token again.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-blueprint" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Blueprint export")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	existing, _ := s.store.ListCodebases(ctx, u.ID)
	have := map[string]int64{}
	for _, c := range existing {
		have[c.Source+"|"+c.Location+"|"+c.Subdir] = c.ID
	}
	counts := map[string]int{"codebases": 0, "notes": 0, "skipped": 0}
	for _, in := range doc.Codebases {
		c := in.Codebase
		key := c.Source + "|" + c.Location + "|" + c.Subdir
		id, ok := have[key]
		if !ok {
			c.ID, c.GitToken = 0, ""
			if c.Source == "path" && (u.Role != "admin" || func() bool { _, err := s.allowedPath(c.Location); return err != nil }()) {
				counts["skipped"]++
				continue
			}
			if c.Source != "git" && c.Source != "path" && c.Source != "files" && c.Source != "upload" {
				counts["skipped"]++
				continue
			}
			if err := s.store.CreateCodebase(ctx, u.ID, &c); err != nil {
				counts["skipped"]++
				continue
			}
			id = c.ID
			have[key] = id
			counts["codebases"]++
		}
		old, _ := s.store.ListNotes(ctx, u.ID, id, "", true)
		seen := map[string]bool{}
		for _, n := range old {
			seen[n.Kind+"|"+n.Path+"|"+n.Text] = true
		}
		for _, n := range in.Notes {
			if seen[n.Kind+"|"+n.Path+"|"+n.Text] || strings.TrimSpace(n.Text) == "" {
				continue
			}
			nn := &store.Note{CodebaseID: id, Path: n.Path, Kind: n.Kind, Text: n.Text, Model: n.Model}
			if nn.Kind != "ai" {
				nn.Kind = "note"
			}
			if s.store.SaveNote(ctx, u.ID, nn) == nil {
				counts["notes"]++
			}
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

// dirSize adds up the files below dir (clones and unpacked archives).
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
