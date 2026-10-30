package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/analyze"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// aiFor is the person's AI: Pocket AI through Core, their own
// OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 150*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 150*time.Second), "openai", ""
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

const aiVoice = `You help someone understand a codebase from what a static scan found: folders and files with their sizes, the functions and types in them, which file imports which, HTTP endpoints, database tables and environment variables, and sometimes the first lines of a few files. Use only what is given; never invent files, functions or behaviour, and say when the data does not show something. Plain words for a developer who is new to this code. Short paragraphs or a few bullets; name files and functions in backticks. No emojis, no filler.`

// digestFor renders what the scan knows about one folder or file as text for
// the model, with the first lines of a few of its files when the code is here.
func (s *Server) digestFor(ctx context.Context, c *store.Codebase, res *analyze.Result, p string) string {
	var b strings.Builder
	st := res.Stats
	fmt.Fprintf(&b, "Codebase %q: %d files, %d lines; languages:", res.Name, st.Files, st.Lines)
	for _, l := range topN(res.Languages, 6) {
		fmt.Fprintf(&b, " %s %d lines;", l.Lang, l.Lines)
	}
	b.WriteString("\n")
	if p == "" {
		b.WriteString("Top-level folders: ")
		for _, d := range topN(topDirs(res, ""), 25) {
			fmt.Fprintf(&b, "%s (%d files, %d lines), ", d.Path, d.Files, d.Lines)
		}
		b.WriteString("\nEntry points: ")
		for _, e := range res.EntryPoints {
			fmt.Fprintf(&b, "%s %s; ", e.Kind, e.Path)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "Looking at: %s\n", p)
	}
	var files []analyze.File
	for _, f := range res.Files {
		if under(f.Path, p) && f.Lang != "" {
			files = append(files, f)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].ImportedBy*50+files[i].Lines > files[j].ImportedBy*50+files[j].Lines
	})
	b.WriteString("Files (path | lines | imported by n files):\n")
	for _, f := range topN(files, 120) {
		fmt.Fprintf(&b, "%s | %d | %d\n", f.Path, f.Lines, f.ImportedBy)
	}
	inSet := map[string]bool{}
	for _, f := range files {
		inSet[f.Path] = true
	}
	n := 0
	b.WriteString("Functions and types (file: kind name — doc):\n")
	for _, sy := range res.Symbols {
		if !inSet[sy.File] || !sy.Exported && p == "" {
			continue
		}
		if n++; n > 260 {
			b.WriteString("…\n")
			break
		}
		name := sy.Name
		if sy.Parent != "" {
			name = sy.Parent + "." + sy.Name
		}
		fmt.Fprintf(&b, "%s: %s %s", sy.File, sy.Kind, name)
		if sy.Doc != "" {
			fmt.Fprintf(&b, " — %s", sy.Doc)
		}
		b.WriteString("\n")
	}
	out, in := map[string]int{}, map[string]int{}
	for _, e := range res.Edges {
		fi, ti := inSet[e.From], inSet[e.To]
		if fi && !ti {
			out[path.Dir(e.To)]++
		} else if ti && !fi {
			in[path.Dir(e.From)]++
		}
	}
	if p != "" {
		b.WriteString("It uses folders: ")
		for _, l := range topN(sortedLinks(out), 20) {
			fmt.Fprintf(&b, "%s (%d), ", l.Path, l.Count)
		}
		b.WriteString("\nUsed by folders: ")
		for _, l := range topN(sortedLinks(in), 20) {
			fmt.Fprintf(&b, "%s (%d), ", l.Path, l.Count)
		}
		b.WriteString("\n")
	}
	n = 0
	for _, e := range res.Endpoints {
		if !under(e.File, p) {
			continue
		}
		if n == 0 {
			b.WriteString("HTTP endpoints:\n")
		}
		if n++; n > 120 {
			b.WriteString("…\n")
			break
		}
		fmt.Fprintf(&b, "%s %s → %s (%s)\n", e.Method, e.Path, e.Handler, e.File)
	}
	for _, t := range res.Tables {
		if p == "" || under(t.File, p) {
			cols := make([]string, 0, len(t.Columns))
			for _, col := range t.Columns {
				cols = append(cols, col.Name)
			}
			fmt.Fprintf(&b, "Table %s: %s\n", t.Name, strings.Join(cols, ", "))
		}
	}
	// the first lines of the most central files, when the code is on disk
	if c != nil {
		root := s.codeRoot(c)
		budget := 14000
		for _, f := range topN(files, 6) {
			if budget <= 0 {
				break
			}
			full := filepath.Join(root, filepath.FromSlash(f.Path))
			data, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			text := string(data)
			if len(text) > 3500 {
				text = text[:3500] + "\n…"
			}
			budget -= len(text)
			fmt.Fprintf(&b, "\n--- %s (start) ---\n%s\n", f.Path, text)
		}
	}
	return b.String()
}

// handleExplain asks the AI what a folder or file is for, and keeps the answer
// as the AI note of that path.
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": why})
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	_ = decode(r, &in)
	p := strings.Trim(in.Path, "/")
	c, err := s.store.GetCodebase(ctx, uid, sc.CodebaseID)
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	what := "this codebase"
	if p != "" {
		what = "`" + p + "`"
	}
	prompt := "Explain " + what + ": what it is for, its main parts and how they fit together, how it connects to the rest (what it uses, what uses it), and anything worth knowing before changing it (loops, very central files, missing docs). At most about 250 words.\n\n" + s.digestFor(ctx, c, res, p)
	text, err := cl.Complete(ctx, aiVoice, prompt, false)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	n := &store.Note{CodebaseID: c.ID, Path: p, Kind: "ai", Text: strings.TrimSpace(text), ScanID: &sc.ID, Model: used}
	if err := s.store.SaveNote(ctx, uid, n); err != nil {
		s.fail(w, err, "save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "note": n})
}

// handleAsk answers a question about a codebase from its latest scan.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	sc, res, ok := s.scanFor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": why})
		return
	}
	var in struct {
		Question string `json:"question"`
		Path     string `json:"path"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		writeErr(w, http.StatusBadRequest, "question is required")
		return
	}
	c, err := s.store.GetCodebase(ctx, uid, sc.CodebaseID)
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	prompt := "Question: " + strings.TrimSpace(in.Question) + "\n\nWhat the scan found:\n" + s.digestFor(ctx, c, res, strings.Trim(in.Path, "/")) +
		"\nAnswer briefly, pointing at the files and functions that matter. If the scan cannot answer it, say what to look at instead."
	text, err := cl.Complete(ctx, aiVoice, prompt, false)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "text": strings.TrimSpace(text)})
}
