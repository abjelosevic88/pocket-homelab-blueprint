package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/config"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// moveLinks starts Blueprint behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (as members).
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "blueprint:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"blueprint","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func (a *app) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/codebases", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/codebases", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 1 || len(p.Imports) != 1 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's codebases to Bob: the import adds once, and nothing of
// one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	m := a.expect(201, a.alice, "POST", "/api/v1/codebases", map[string]any{"source": "git", "location": "https://github.com/example/alice-repo", "git_token": "secret", "scan": false})
	cid := int64(m["id"].(float64))
	a.expect(201, a.alice, "POST", fmt.Sprintf("/api/v1/codebases/%d/notes", cid), map[string]any{"path": "cmd", "text": "Start here"})

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "alice-repo") || !strings.Contains(string(export), "Start here") || strings.Contains(string(export), "secret") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "alice-repo") {
		t.Fatalf("bob's export holds alice's codebase: %s", bob)
	}

	var first, second map[string]any
	for i, out := range []*map[string]any{&first, &second} {
		code, body := a.raw("pcx_bob_import", "POST", "/api/v1/import", "application/json", export)
		if code != 200 {
			t.Fatalf("import %d → %d %s", i, code, body)
		}
		_ = json.Unmarshal(body, out)
	}
	if first["codebases"] != 1.0 || first["notes"] != 1.0 || second["codebases"] != 0.0 || second["notes"] != 0.0 {
		t.Fatalf("imports: %v then %v", first, second)
	}

	_, _, bobList := a.call(a.bob, "GET", "/api/v1/codebases", nil)
	_, _, aliceList := a.call(a.alice, "GET", "/api/v1/codebases", nil)
	if bl, _ := a.st.OwnCodebases(t.Context(), a.ids["Bob"]); len(bl) != 1 {
		t.Fatalf("bob owns %d codebases (%v)", len(bl), bobList)
	}
	if al, _ := a.st.OwnCodebases(t.Context(), a.ids["Alice"]); len(al) != 1 {
		t.Fatalf("alice owns %d codebases (%v)", len(al), aliceList)
	}
}
