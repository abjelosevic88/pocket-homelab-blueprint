package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/config"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

type app struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	cfg   *config.Config
	alice string
	bob   string
	ids   map[string]int64
}

func newApp(t *testing.T, tweak func(*config.Config)) *app {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.BaseURL = "http://blueprint.test"
	if tweak != nil {
		tweak(cfg)
	}
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &app{t: t, st: st, cfg: cfg, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *app) call(auth, method, path string, body any) (int, map[string]any, []any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return res.StatusCode, m, l
}

func (a *app) text(auth, path string) (int, string) {
	a.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.url+path, nil)
	req.Header.Set("Authorization", "Bearer "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (a *app) ok(auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code >= 300 {
		a.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (a *app) expect(want int, auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code != want {
		a.t.Fatalf("%s %s → %d, want %d (%v)", method, path, code, want, m)
	}
	return m
}

// zipOf builds a zip archive from path → content.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for p, body := range files {
		w, err := zw.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload posts an archive; codebase > 0 replaces that codebase's code.
func (a *app) upload(auth string, data []byte, name string, codebase int64) (int, map[string]any) {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if name != "" {
		_ = mw.WriteField("name", name)
	}
	if codebase > 0 {
		_ = mw.WriteField("codebase", fmt.Sprint(codebase))
	}
	fw, _ := mw.CreateFormFile("file", "project.zip")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, a.url+"/api/v1/codebases/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

// waitScan waits until the codebase's latest scan has finished and returns its id.
func (a *app) waitScan(auth string, cid int64) int64 {
	a.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		c := a.ok(auth, "GET", fmt.Sprintf("/api/v1/codebases/%d", cid), nil)
		if l, ok := c["latest"].(map[string]any); ok {
			switch l["status"] {
			case "done":
				return int64(l["id"].(float64))
			case "failed", "cancelled":
				a.t.Fatalf("scan %v: %v", l["status"], l["error"])
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.t.Fatal("scan did not finish")
	return 0
}

var sampleV1 = map[string]string{
	"shop/go.mod": "module example.com/shop\n\ngo 1.22\n\nrequire github.com/go-chi/chi/v5 v5.0.12\n",
	"shop/main.go": `package main

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"example.com/shop/store"
)

func main() {
	st := store.New(os.Getenv("DATABASE_URL"))
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Get("/items", func(w http.ResponseWriter, r *http.Request) { _ = st.Items() })
		r.Post("/items/{id}/buy", buy)
	})
	http.ListenAndServe(":"+os.Getenv("PORT"), r)
}

func buy(w http.ResponseWriter, r *http.Request) {}
`,
	"shop/store/store.go": `// Package store keeps items.
package store

// Store holds items.
type Store struct{ url string }

// New opens the store.
func New(url string) *Store { return &Store{url: url} }

// Items lists the items.
func (s *Store) Items() []string { return nil }
`,
	"shop/migrations/0001.sql": "CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\nCREATE TABLE orders (id INTEGER PRIMARY KEY, item_id INTEGER REFERENCES items(id));\n",
	"shop/.env.example":        "PORT=8080\n",
}

func TestUploadScanAndViews(t *testing.T) {
	a := newApp(t, nil)
	code, c := a.upload(a.alice, zipOf(t, sampleV1), "Shop", 0)
	if code != http.StatusCreated {
		t.Fatalf("upload → %d %v", code, c)
	}
	cid := int64(c["id"].(float64))
	sid := a.waitScan(a.alice, cid)

	ov := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/overview", sid), nil)
	stats := ov["stats"].(map[string]any)
	if stats["files"].(float64) < 4 || stats["tables"].(float64) != 2 {
		t.Fatalf("stats = %v", stats)
	}
	// the single top folder of the archive ("shop/") is dropped
	tree := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/tree", sid), nil)
	found := false
	for _, f := range tree["files"].([]any) {
		if f.(map[string]any)["path"] == "store/store.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("store/store.go not in the tree: %v", tree["files"])
	}
	eps := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/endpoints", sid), nil)
	var paths []string
	for _, e := range eps["endpoints"].([]any) {
		m := e.(map[string]any)
		paths = append(paths, m["method"].(string)+" "+m["path"].(string))
	}
	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "GET /api/items") || !strings.Contains(joined, "POST /api/items/{id}/buy") {
		t.Fatalf("endpoints = %v", paths)
	}
	code, spec := a.text(a.alice, fmt.Sprintf("/api/v1/scans/%d/openapi.json", sid))
	if code != 200 || !strings.Contains(spec, `"/api/items/{id}/buy"`) || !strings.Contains(spec, `"openapi": "3.1.0"`) {
		t.Fatalf("openapi.json → %d %s", code, spec)
	}
	code, y := a.text(a.alice, fmt.Sprintf("/api/v1/scans/%d/openapi.yaml", sid))
	if code != 200 || !strings.Contains(y, "openapi: 3.1.0") || !strings.Contains(y, "/api/items/{id}/buy:") {
		t.Fatalf("openapi.yaml → %d %s", code, y)
	}
	g := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/graph?level=dir&depth=2", sid), nil)
	if len(g["edges"].([]any)) == 0 {
		t.Fatalf("graph has no edges: %v", g)
	}
	file := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/file?path=store/store.go", sid), nil)
	if file["kind"] != "file" || len(file["symbols"].([]any)) < 2 {
		t.Fatalf("file detail = %v", file)
	}
	src := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/source?path=store/store.go", sid), nil)
	if !strings.Contains(src["text"].(string), "package store") {
		t.Fatalf("source = %v", src)
	}
	a.expect(404, a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/source?path=../../t.db", sid), nil)
	data := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/data", sid), nil)
	if rels := data["relations"].([]any); len(rels) != 1 || rels[0].(map[string]any)["to"] != "items.id" {
		t.Fatalf("relations = %v", data["relations"])
	}
	cfg := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/config", sid), nil)
	undocumented := map[string]bool{}
	for _, v := range cfg["env_vars"].([]any) {
		m := v.(map[string]any)
		undocumented[m["name"].(string)] = !m["documented"].(bool)
	}
	if undocumented["PORT"] || !undocumented["DATABASE_URL"] {
		t.Fatalf("env vars = %v", cfg["env_vars"])
	}
	code, md := a.text(a.alice, fmt.Sprintf("/api/v1/scans/%d/report.md", sid))
	if code != 200 || !strings.Contains(md, "# Shop") || !strings.Contains(md, "`/api/items`") {
		t.Fatalf("report.md → %d %s", code, md)
	}
	hits := a.ok(a.alice, "GET", "/api/v1/codebases", nil)
	_ = hits
	_, _, list := a.call(a.alice, "GET", "/api/v1/search?q=Items", nil)
	if len(list) == 0 {
		t.Fatal("search found nothing for Items")
	}

	// a new archive → a second scan → compare
	v2 := map[string]string{}
	for k, v := range sampleV1 {
		v2[k] = v
	}
	v2["shop/migrations/0002.sql"] = "CREATE TABLE coupons (id INTEGER PRIMARY KEY, code TEXT UNIQUE);\nALTER TABLE orders ADD COLUMN coupon_id INTEGER REFERENCES coupons(id);\n"
	if code, m := a.upload(a.alice, zipOf(t, v2), "", cid); code != http.StatusCreated {
		t.Fatalf("re-upload → %d %v", code, m)
	}
	sid2 := a.waitScan(a.alice, cid)
	if sid2 == sid {
		t.Fatal("no new scan")
	}
	diff := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/compare/%d", sid2, sid), nil)
	tables := diff["tables"].(map[string]any)
	if fmt.Sprint(tables["added"]) != "[coupons]" || len(tables["changed"].([]any)) != 1 {
		t.Fatalf("compare tables = %v", tables)
	}
}

// Everything is strictly per person: Bob gets 404 for Alice's codebase,
// scans and notes, and never sees them in lists or search.
func TestPerPersonIsolation(t *testing.T) {
	a := newApp(t, nil)
	_, c := a.upload(a.alice, zipOf(t, sampleV1), "Alice's shop", 0)
	cid := int64(c["id"].(float64))
	sid := a.waitScan(a.alice, cid)
	note := a.ok(a.alice, "POST", fmt.Sprintf("/api/v1/codebases/%d/notes", cid), map[string]any{"path": "store", "text": "mine"})
	nid := int64(note["id"].(float64))

	for _, p := range []string{
		fmt.Sprintf("/api/v1/codebases/%d", cid), fmt.Sprintf("/api/v1/codebases/%d/scans", cid), fmt.Sprintf("/api/v1/codebases/%d/notes", cid),
		fmt.Sprintf("/api/v1/scans/%d", sid), fmt.Sprintf("/api/v1/scans/%d/overview", sid), fmt.Sprintf("/api/v1/scans/%d/tree", sid),
		fmt.Sprintf("/api/v1/scans/%d/file?path=store", sid), fmt.Sprintf("/api/v1/scans/%d/source?path=main.go", sid),
		fmt.Sprintf("/api/v1/scans/%d/graph", sid), fmt.Sprintf("/api/v1/scans/%d/openapi.json", sid), fmt.Sprintf("/api/v1/scans/%d/export.json", sid),
	} {
		a.expect(404, a.bob, "GET", p, nil)
	}
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/codebases/%d", cid), map[string]any{"name": "taken"})
	a.expect(404, a.bob, "POST", fmt.Sprintf("/api/v1/codebases/%d/scan", cid), nil)
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/codebases/%d", cid), nil)
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/scans/%d", sid), nil)
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/notes/%d", nid), map[string]any{"text": "x"})
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/notes/%d", nid), nil)
	a.expect(404, a.bob, "POST", fmt.Sprintf("/api/v1/scans/%d/explain", sid), map[string]any{})
	if code, m := a.upload(a.bob, zipOf(t, sampleV1), "", cid); code != 404 {
		t.Fatalf("bob replacing alice's archive → %d %v", code, m)
	}
	_, _, list := a.call(a.bob, "GET", "/api/v1/codebases", nil)
	if len(list) != 0 {
		t.Fatalf("bob sees %v", list)
	}
	_, _, hits := a.call(a.bob, "GET", "/api/v1/search?q=Items", nil)
	if len(hits) != 0 {
		t.Fatalf("bob's search finds alice's code: %v", hits)
	}
	// Alice's things are untouched
	if got := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/codebases/%d", cid), nil); got["name"] != "Alice's shop" {
		t.Fatalf("alice's codebase = %v", got)
	}
}

// Sharing: Alice shares a codebase with Bob to look at; he reads every scan
// and keeps his own notes, but cannot change, delete, share or scan it until
// she lets him scan; then she stops sharing and it is gone for him.
func TestSharing(t *testing.T) {
	a := newApp(t, nil)
	_, c := a.upload(a.alice, zipOf(t, sampleV1), "Alice's shop", 0)
	cid := int64(c["id"].(float64))
	sid := a.waitScan(a.alice, cid)
	a.ok(a.alice, "POST", fmt.Sprintf("/api/v1/codebases/%d/notes", cid), map[string]any{"path": "store", "text": "alice's"})
	bob, alice := a.ids["Bob"], a.ids["Alice"]

	_, _, people := a.call(a.alice, "GET", "/api/v1/people", nil)
	if len(people) != 1 || people[0].(map[string]any)["email"] != "bob@example.com" {
		t.Fatalf("people = %v", people)
	}
	a.expect(400, a.alice, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), map[string]any{"role": "edit"})
	a.expect(400, a.alice, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, alice), map[string]any{"role": "view"})
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), map[string]any{"role": "view"})
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), map[string]any{"role": "view"})

	_, _, shares := a.call(a.alice, "GET", fmt.Sprintf("/api/v1/codebases/%d/shares", cid), nil)
	if len(shares) != 1 || shares[0].(map[string]any)["role"] != "view" {
		t.Fatalf("shares = %v", shares)
	}
	_, _, list := a.call(a.bob, "GET", "/api/v1/codebases", nil)
	if len(list) != 1 {
		t.Fatalf("bob's list = %v", list)
	}
	if got := list[0].(map[string]any); got["role"] != "view" || got["owner"] != "Alice" || got["done"] == nil {
		t.Fatalf("shared codebase = %v", got)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/v1/codebases/%d", cid), fmt.Sprintf("/api/v1/codebases/%d/scans", cid),
		fmt.Sprintf("/api/v1/scans/%d", sid), fmt.Sprintf("/api/v1/scans/%d/overview", sid), fmt.Sprintf("/api/v1/scans/%d/tree", sid),
		fmt.Sprintf("/api/v1/scans/%d/file?path=store", sid), fmt.Sprintf("/api/v1/scans/%d/graph", sid),
		fmt.Sprintf("/api/v1/scans/%d/openapi.json", sid), fmt.Sprintf("/api/v1/scans/%d/report.md", sid),
	} {
		a.expect(200, a.bob, "GET", p, nil)
	}
	_, _, hits := a.call(a.bob, "GET", "/api/v1/search?q=Items", nil)
	if len(hits) == 0 {
		t.Fatal("bob's search does not find the shared code")
	}
	// notes stay personal
	a.ok(a.bob, "POST", fmt.Sprintf("/api/v1/codebases/%d/notes", cid), map[string]any{"path": "store", "text": "bob's"})
	_, _, notes := a.call(a.bob, "GET", fmt.Sprintf("/api/v1/codebases/%d/notes", cid), nil)
	if len(notes) != 1 || notes[0].(map[string]any)["text"] != "bob's" {
		t.Fatalf("bob's notes = %v", notes)
	}
	// owner-only things
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/codebases/%d", cid), map[string]any{"name": "taken"})
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/codebases/%d", cid), nil)
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/scans/%d", sid), nil)
	a.expect(404, a.bob, "GET", fmt.Sprintf("/api/v1/codebases/%d/shares", cid), nil)
	a.expect(403, a.bob, "POST", fmt.Sprintf("/api/v1/codebases/%d/scan", cid), nil)

	// allowed to scan: the scan is Alice's
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), map[string]any{"role": "scan"})
	a.ok(a.bob, "POST", fmt.Sprintf("/api/v1/codebases/%d/scan", cid), nil)
	if sid2 := a.waitScan(a.alice, cid); sid2 == sid {
		t.Fatal("bob's scan did not run")
	}

	// Bob leaves; Alice shares again and stops sharing
	a.ok(a.bob, "DELETE", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), nil)
	a.expect(404, a.bob, "GET", fmt.Sprintf("/api/v1/codebases/%d", cid), nil)
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), map[string]any{"role": "view"})
	a.ok(a.alice, "DELETE", fmt.Sprintf("/api/v1/codebases/%d/shares/%d", cid, bob), nil)
	a.expect(404, a.bob, "GET", fmt.Sprintf("/api/v1/scans/%d/overview", sid), nil)
	if _, _, list := a.call(a.bob, "GET", "/api/v1/codebases", nil); len(list) != 0 {
		t.Fatalf("bob still sees %v", list)
	}
}

// Server folders: only admins, and only inside SOURCE_ROOTS.
func TestServerFoldersAdminOnly(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(proj, "main.py"), []byte("import os\nprint(os.getenv('HOME'))\n"), 0o644)
	a := newApp(t, func(c *config.Config) { c.SourceRoots = []string{root} })
	a.expect(400, a.bob, "POST", "/api/v1/codebases", map[string]any{"source": "path", "location": proj})
	a.expect(400, a.alice, "POST", "/api/v1/codebases", map[string]any{"source": "path", "location": t.TempDir()})
	a.expect(400, a.alice, "POST", "/api/v1/codebases", map[string]any{"source": "path", "location": proj + "/../../"})
	c := a.expect(201, a.alice, "POST", "/api/v1/codebases", map[string]any{"source": "path", "location": proj})
	a.waitScan(a.alice, int64(c["id"].(float64)))
	a.expect(403, a.bob, "GET", "/api/v1/sources/browse", nil)
	b := a.ok(a.alice, "GET", "/api/v1/sources/browse?path="+root, nil)
	if len(b["dirs"].([]any)) != 1 {
		t.Fatalf("browse = %v", b)
	}
}

func TestGitURLCheck(t *testing.T) {
	for _, bad := range []string{"git@github.com:a/b.git", "ssh://git@x/y", "file:///etc", "ext::sh -c id", "https://user:pw@github.com/a/b", "github.com/a/b"} {
		if _, err := checkGitURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"https://github.com/go-chi/chi", "http://git.local:3000/me/repo.git"} {
		if _, err := checkGitURL(good); err != nil {
			t.Errorf("%q refused: %v", good, err)
		}
	}
	if got := redactURL("fatal: https://x-access-token:abc@github.com/a/b"); strings.Contains(got, "abc") {
		t.Errorf("token not redacted: %s", got)
	}
}

// An archive cannot write outside its folder.
func TestUnpackStaysInside(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(arc, zipOf(t, map[string]string{"../evil.txt": "x", "ok/a.go": "package a\n", "/abs.txt": "y"}), 0o644)
	dst := filepath.Join(dir, "out", "src")
	if err := unpack(arc, dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "evil.txt")); err == nil {
		t.Fatal("../evil.txt was written outside")
	}
	if _, err := os.Stat(filepath.Join(dst, "abs.txt")); err != nil {
		t.Fatalf("abs.txt should land inside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "ok", "a.go")); err != nil {
		t.Fatalf("ok/a.go missing: %v", err)
	}
}

// Link tokens only reach the routes of their scope.
func TestLinkScopes(t *testing.T) {
	req := func(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }
	read := []string{"blueprint:read"}
	write := []string{"blueprint:write"}
	cases := []struct {
		scopes []string
		r      *http.Request
		want   bool
	}{
		{read, req("GET", "/api/v1/codebases"), true},
		{read, req("GET", "/api/v1/scans/4/overview"), true},
		{read, req("GET", "/api/v1/scans/4/openapi.yaml"), true},
		{read, req("GET", "/api/v1/scans/4/compare/3"), true},
		{read, req("GET", "/api/v1/search"), true},
		{read, req("POST", "/api/v1/codebases/4/scan"), false},
		{read, req("GET", "/api/v1/scans/4/source"), false},
		{read, req("GET", "/api/v1/settings"), false},
		{write, req("POST", "/api/v1/codebases"), true},
		{write, req("POST", "/api/v1/codebases/4/scan"), true},
		{write, req("DELETE", "/api/v1/codebases/4"), false},
		{write, req("PUT", "/api/v1/codebases/4"), false},
		{write, req("GET", "/api/v1/scans/4/tree"), false},
		{nil, req("GET", "/api/v1/auth/me"), true},
	}
	for _, c := range cases {
		if got := linkAllows(c.scopes, c.r); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Export then import into another account adds the codebases once.
func TestExportImport(t *testing.T) {
	a := newApp(t, nil)
	a.expect(201, a.alice, "POST", "/api/v1/codebases", map[string]any{"source": "git", "location": "https://github.com/example/repo", "git_token": "secret", "scan": false})
	c := a.ok(a.alice, "GET", "/api/v1/codebases", nil)
	_ = c
	code, raw := a.text(a.alice, "/api/v1/export.json")
	if code != 200 || strings.Contains(raw, "secret") {
		t.Fatalf("export → %d, leaks the token: %v", code, strings.Contains(raw, "secret"))
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(raw), &doc)
	first := a.ok(a.bob, "POST", "/api/v1/import", doc)
	second := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if first["codebases"].(float64) != 1 || second["codebases"].(float64) != 0 {
		t.Fatalf("import = %v then %v", first, second)
	}
}

// Settings are per person.
func TestSettingsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"keep_scans": 3, "graph_layout": "organic"})
	if m := a.ok(a.bob, "GET", "/api/v1/settings", nil); m["keep_scans"].(float64) != 10 || m["graph_layout"] != "layers" {
		t.Fatalf("bob's settings changed: %v", m)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"graph_layout": "spiral"})
}

// The demo codebase is scanned twice and has every page filled.
func TestDemoData(t *testing.T) {
	a := newApp(t, func(c *config.Config) { c.DemoData = true })
	if err := a.s.ownerReady(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	_, _, list := a.call(a.alice, "GET", "/api/v1/codebases", nil)
	if len(list) != 1 {
		t.Fatalf("codebases = %v", list)
	}
	c := list[0].(map[string]any)
	cid := int64(c["id"].(float64))
	_, _, scans := a.call(a.alice, "GET", fmt.Sprintf("/api/v1/codebases/%d/scans", cid), nil)
	if len(scans) != 2 {
		t.Fatalf("scans = %d", len(scans))
	}
	sid := int64(c["done"].(map[string]any)["id"].(float64))
	ov := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/scans/%d/overview", sid), nil)
	st := ov["stats"].(map[string]any)
	if st["endpoints"].(float64) < 15 || st["tables"].(float64) < 5 || st["cycles"].(float64) < 1 {
		t.Fatalf("demo stats = %v", st)
	}
}
