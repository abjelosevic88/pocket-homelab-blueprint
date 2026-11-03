package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- codebases ----

func (s *Server) handleListCodebases(w http.ResponseWriter, r *http.Request) {
	list, err := s.listCodebases(r.Context())
	if err != nil {
		s.fail(w, err, "codebases")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetCodebase(w http.ResponseWriter, r *http.Request) {
	c, err := s.codebaseFor(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type codebaseInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Source      string  `json:"source"`
	Location    *string `json:"location"`
	Branch      *string `json:"branch"`
	Subdir      *string `json:"subdir"`
	GitUser     *string `json:"git_user"`
	GitToken    *string `json:"git_token"`
	Vendored    *bool   `json:"vendored"`
	RescanHours *int    `json:"rescan_hours"`
	Pinned      *bool   `json:"pinned"`
	Scan        *bool   `json:"scan"` // after saving (default yes on create)
}

// apply copies the sent fields onto c and checks them; msg is for the person.
func (s *Server) applyCodebase(ctx context.Context, u *store.User, c *store.Codebase, in codebaseInput, creating bool) string {
	str := func(p *string, dst *string) {
		if p != nil {
			*dst = strings.TrimSpace(*p)
		}
	}
	str(in.Name, &c.Name)
	str(in.Description, &c.Description)
	str(in.Location, &c.Location)
	str(in.Branch, &c.Branch)
	str(in.GitUser, &c.GitUser)
	if in.GitToken != nil && !strings.Contains(*in.GitToken, "••") {
		c.GitToken = strings.TrimSpace(*in.GitToken)
	}
	if in.Subdir != nil {
		sub, err := cleanSubdir(*in.Subdir)
		if err != nil {
			return err.Error()
		}
		c.Subdir = sub
	}
	if in.Vendored != nil {
		c.Vendored = *in.Vendored
	}
	if in.Pinned != nil {
		c.Pinned = *in.Pinned
	}
	if in.RescanHours != nil {
		h := *in.RescanHours
		if h < 0 || h > 24*30 {
			return "scan again every 0 (never) to 720 hours"
		}
		c.RescanHours = h
	}
	if strings.ContainsAny(c.Branch, " ~^:?*[\\") || strings.HasPrefix(c.Branch, "-") {
		return "that is not a branch name"
	}
	switch c.Source {
	case "git":
		loc, err := checkGitURL(c.Location)
		if err != nil {
			return err.Error()
		}
		c.Location = strings.TrimSuffix(loc, "/")
		if c.Name == "" {
			c.Name = strings.TrimSuffix(path.Base(c.Location), ".git")
		}
	case "path":
		if u.Role != "admin" {
			return "only admins can scan folders on the server"
		}
		p, err := s.allowedPath(c.Location)
		if err != nil {
			return err.Error()
		}
		c.Location = p
		if c.Name == "" {
			c.Name = filepath.Base(p)
		}
	case "files":
		space, p := filesLocation(c.Location)
		if space == "" || p == "" {
			return "pick a folder in Pocket Files"
		}
		c.Location = space + ":" + path.Clean(p)
		if c.Name == "" {
			c.Name = path.Base(path.Clean(p))
		}
	case "upload":
		if creating {
			return "upload an archive to add it"
		}
	default:
		return "pick where the code comes from"
	}
	if c.Name == "" || c.Name == "/" || c.Name == "." {
		c.Name = "Codebase"
	}
	if len(c.Name) > 120 {
		c.Name = c.Name[:120]
	}
	return ""
}

func (s *Server) handleCreateCodebase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in codebaseInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if app := linkApp(ctx); app != "" && in.Source != "git" {
		writeErr(w, http.StatusForbidden, "an app may only add git repositories")
		return
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	c := &store.Codebase{Source: in.Source, Vendored: st.Vendored}
	if msg := s.applyCodebase(ctx, u, c, in, true); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateCodebase(ctx, u.ID, c); err != nil {
		s.fail(w, err, "add codebase")
		return
	}
	if in.Scan == nil || *in.Scan {
		trigger := "new"
		if app := linkApp(ctx); app != "" {
			trigger = "link:" + app
		}
		if _, _, err := s.store.QueueScan(ctx, u.ID, c.ID, trigger); err == nil {
			s.nudge()
		}
	}
	c, _ = s.store.GetCodebase(ctx, u.ID, c.ID)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateCodebase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	c, err := s.store.GetCodebase(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	var in codebaseInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	before := *c
	if msg := s.applyCodebase(ctx, u, c, in, false); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateCodebase(ctx, u.ID, c); err != nil {
		s.fail(w, err, "save codebase")
		return
	}
	if c.Source == "git" && (before.Location != c.Location || before.Branch != c.Branch) {
		s.removeWork(c.ID) // clone again from the new place
	}
	if in.Scan != nil && *in.Scan {
		if _, _, err := s.store.QueueScan(ctx, u.ID, c.ID, "manual"); err == nil {
			s.nudge()
		}
	}
	c, _ = s.store.GetCodebase(ctx, u.ID, c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCodebase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := idParam(r, "id")
	c, err := s.store.GetCodebase(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	if c.Latest != nil && (c.Latest.Status == "running" || c.Latest.Status == "queued") {
		_ = s.store.CancelScan(ctx, uid, c.Latest.ID)
		s.cancelScan(c.Latest.ID)
	}
	if err := s.store.DeleteCodebase(ctx, uid, id); err != nil {
		s.fail(w, err, "delete codebase")
		return
	}
	s.removeWork(id)
	w.WriteHeader(http.StatusNoContent)
}

// handleScanCodebase queues a new scan (or returns the one already waiting).
// Its owner and those it is shared with to scan may; the scan is the owner's.
func (s *Server) handleScanCodebase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.codebaseFor(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	if !mayScan(c) {
		writeErr(w, http.StatusForbidden, "this codebase is shared with you to look at; ask "+c.Owner+" to scan it")
		return
	}
	uid := c.UserID
	trigger := "manual"
	if app := linkApp(ctx); app != "" {
		trigger = "link:" + app
	}
	sc, created, err := s.store.QueueScan(ctx, uid, c.ID, trigger)
	if err != nil {
		s.fail(w, err, "scan")
		return
	}
	s.nudge()
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, sc)
}

// handleUpload adds a codebase from an archive, or (with ?codebase=id)
// replaces an uploaded codebase's code and scans it again.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	limit := s.cfg.MaxUploadMB << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send the archive as multipart/form-data")
		return
	}
	fields := map[string]string{}
	var tmpPath, fileName string
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the archive is larger than %d MB", s.cfg.MaxUploadMB))
			return
		}
		if part.FormName() == "file" {
			if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "work"), 0o755); err != nil {
				s.fail(w, err, "upload")
				return
			}
			f, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "work"), "upload-*")
			if err != nil {
				s.fail(w, err, "upload")
				return
			}
			tmpPath, fileName = f.Name(), filepath.Base(part.FileName())
			n, err := io.Copy(f, io.LimitReader(part, limit+1))
			f.Close()
			if err != nil || n > limit {
				writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the archive is larger than %d MB", s.cfg.MaxUploadMB))
				return
			}
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(part, 4096))
		fields[part.FormName()] = strings.TrimSpace(string(b))
	}
	if tmpPath == "" {
		writeErr(w, http.StatusBadRequest, "choose a .zip or .tar.gz file")
		return
	}
	var c *store.Codebase
	if id := fields["codebase"]; id != "" {
		var cid int64
		fmt.Sscan(id, &cid)
		if c, err = s.store.GetCodebase(ctx, u.ID, cid); err != nil {
			s.fail(w, err, "codebase")
			return
		}
		if c.Source != "upload" {
			writeErr(w, http.StatusBadRequest, "only an uploaded codebase takes a new archive")
			return
		}
	} else {
		st, _ := s.store.GetSettings(ctx, u.ID)
		name := fields["name"]
		if name == "" {
			name = archiveBase(fileName)
		}
		c = &store.Codebase{Name: name, Source: "upload", Location: fileName, Vendored: st.Vendored, Description: fields["description"]}
		if sub, err := cleanSubdir(fields["subdir"]); err == nil {
			c.Subdir = sub
		}
		if len(c.Name) > 120 {
			c.Name = c.Name[:120]
		}
		if err := s.store.CreateCodebase(ctx, u.ID, c); err != nil {
			s.fail(w, err, "add codebase")
			return
		}
	}
	if err := unpack(tmpPath, filepath.Join(s.workDir(c.ID), "src"), limit*8); err != nil {
		if fields["codebase"] == "" {
			_ = s.store.DeleteCodebase(ctx, u.ID, c.ID)
			s.removeWork(c.ID)
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if fields["codebase"] != "" && fileName != "" {
		c.Location = fileName
		_ = s.store.UpdateCodebase(ctx, u.ID, c)
	}
	if _, _, err := s.store.QueueScan(ctx, u.ID, c.ID, "new"); err == nil {
		s.nudge()
	}
	c, _ = s.store.GetCodebase(ctx, u.ID, c.ID)
	writeJSON(w, http.StatusCreated, c)
}

func archiveBase(name string) string {
	n := filepath.Base(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(strings.ToLower(n), ext) {
			return n[:len(n)-len(ext)]
		}
	}
	return n
}

// ---- scans ----

func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.codebaseFor(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	list, err := s.store.ListScans(ctx, c.UserID, c.ID)
	if err != nil {
		s.fail(w, err, "scans")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCancelScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	sc, c, err := s.scanAccess(ctx, id)
	if err != nil || !mayScan(c) {
		s.fail(w, store.ErrNotFound, "cancel")
		return
	}
	if err := s.store.CancelScan(ctx, c.UserID, sc.ID); err != nil {
		s.fail(w, err, "cancel")
		return
	}
	s.cancelScan(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if err := s.store.DeleteScan(ctx, actorID(ctx), id); err != nil {
		s.fail(w, err, "delete scan")
		return
	}
	s.results.drop(id)
	w.WriteHeader(http.StatusNoContent)
}

// ---- picking a source ----

// handleSources says which sources this person can use.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	_, via, detail := s.connStatus(ctx, u, st, appFiles)
	_, gitErr := exec.LookPath("git")
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   map[string]any{"available": u.Role == "admin" && len(s.cfg.SourceRoots) > 0, "admin": u.Role == "admin", "roots": s.rootsFor(u)},
		"git":    map[string]any{"available": gitErr == nil},
		"files":  map[string]any{"available": via != "", "detail": detail},
		"upload": map[string]any{"available": true, "max_mb": s.cfg.MaxUploadMB},
	})
}

func (s *Server) rootsFor(u *store.User) []string {
	if u.Role != "admin" {
		return []string{}
	}
	out := append([]string{}, s.cfg.SourceRoots...)
	return out
}

type dirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Project string `json:"project,omitempty"` // what marks it as a project: go.mod, package.json, .git…
}

var projectMarkers = []string{".git", "go.mod", "package.json", "pyproject.toml", "requirements.txt", "Cargo.toml", "pom.xml", "build.gradle", "composer.json", "Gemfile", "Makefile"}

// handleBrowse lists the folders inside a server folder (admins only).
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		out := []dirEntry{}
		for _, root := range s.cfg.SourceRoots {
			out = append(out, dirEntry{Name: root, Path: root, Project: marker(root)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "parent": "", "dirs": out})
		return
	}
	real, err := s.allowedPath(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ents, err := os.ReadDir(real)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read this folder")
		return
	}
	out := []dirEntry{}
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(p, e.Name())
		out = append(out, dirEntry{Name: e.Name(), Path: full, Project: marker(filepath.Join(real, e.Name()))})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(p)
	if _, err := s.allowedPath(parent); err != nil {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "dirs": out, "project": marker(real)})
}

func marker(dir string) string {
	for _, m := range projectMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return m
		}
	}
	return ""
}

// handleFilesBrowse lists folders in the person's Pocket Files.
func (s *Server) handleFilesBrowse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	sib, err := s.siblingFor(ctx, u, st, appFiles, "files:read")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	space := r.URL.Query().Get("space")
	if space == "" {
		var spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		}
		if err := sib.client.Do(ctx, http.MethodGet, "/api/v1/spaces", nil, nil, &spaces); err != nil {
			writeErr(w, http.StatusBadGateway, "Pocket Files: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
		return
	}
	var listing struct {
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	q := url.Values{"space": {space}, "path": {r.URL.Query().Get("path")}}
	if err := sib.client.Do(ctx, http.MethodGet, "/api/v1/fs/list", q, nil, &listing); err != nil {
		writeErr(w, http.StatusBadGateway, "Pocket Files: "+err.Error())
		return
	}
	out := []dirEntry{}
	marks := map[string]bool{}
	for _, m := range projectMarkers {
		marks[m] = true
	}
	project := ""
	for _, e := range listing.Entries {
		if marks[e.Name] && project == "" {
			project = e.Name
		}
		if e.IsDir && !strings.HasPrefix(e.Name, ".") {
			out = append(out, dirEntry{Name: e.Name, Path: e.Path})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": space, "path": listing.Path, "dirs": out, "project": project})
}
