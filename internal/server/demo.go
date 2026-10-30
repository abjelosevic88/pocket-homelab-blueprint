package server

import (
	"context"
	"embed"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

//go:embed demo/*.txtar
var demoFS embed.FS

// txtar reads the simple archive format the demo code is kept in: a
// comment, then "-- path --" lines each followed by that file's content.
func txtar(name string) map[string]string {
	b, _ := demoFS.ReadFile("demo/" + name)
	out := map[string]string{}
	cur := ""
	var buf strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = buf.String()
		}
		buf.Reset()
	}
	for _, line := range strings.SplitAfter(string(b), "\n") {
		t := strings.TrimRight(line, "\n")
		if strings.HasPrefix(t, "-- ") && strings.HasSuffix(t, " --") && len(t) > 6 {
			flush()
			cur = strings.TrimSpace(t[3 : len(t)-3])
			continue
		}
		if cur != "" {
			buf.WriteString(line)
		}
	}
	flush()
	return out
}

func writeFiles(root string, files map[string]string) error {
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

var demoAuthors = []string{"Maja Petrović <maja@northwind.example>", "Tom Becker <tom@northwind.example>", "Lena Ortiz <lena@northwind.example>", "Arjun Rao <arjun@northwind.example>"}

var demoMessages = []string{"Fix the order total when an item is out of stock", "Clearer error when signing in fails", "Small refactor of the handlers", "Validate the cart before checkout",
	"Log slow queries", "Rename variables for readability", "Handle an empty search", "Tidy up the router", "Return 404 for unknown products", "Keep the cart after reloading",
	"Make the order list faster", "Guard against negative stock", "Better messages for the webhook", "Retry e-mails that fail", "Round discounts the same way everywhere"}

// demoHistory makes git commits with plausible dates and authors so the
// hotspots and the history have something to show. Each commit touches one
// of the busy files with a trailing comment; a last commit restores them.
type demoHistory struct {
	dir   string
	rng   *rand.Rand
	when  time.Time
	ok    bool
	files map[string]string
}

func (h *demoHistory) git(author string, at time.Time, args ...string) error {
	name, email, _ := strings.Cut(author, " <")
	cmd := exec.Command("git", append([]string{"-c", "user.name=" + name, "-c", "user.email=" + strings.TrimSuffix(email, ">"), "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = h.dir
	date := at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func (h *demoHistory) commit(msg string, author string) {
	if !h.ok {
		return
	}
	if err := h.git(author, h.when, "add", "-A"); err == nil {
		_ = h.git(author, h.when, "commit", "-q", "--allow-empty", "-m", msg)
	}
}

func commentFor(p string) string {
	switch filepath.Ext(p) {
	case ".py", ".toml", ".yaml", ".yml":
		return "# "
	case ".sql":
		return "-- "
	}
	return "// "
}

// churn makes n commits over days, each touching one of the weighted files.
func (h *demoHistory) churn(n, days int, weighted []string) {
	if !h.ok {
		return
	}
	step := time.Duration(days) * 24 * time.Hour / time.Duration(n+1)
	for i := 0; i < n; i++ {
		h.when = h.when.Add(step + time.Duration(h.rng.Intn(5*3600))*time.Second)
		p := weighted[h.rng.Intn(len(weighted))]
		body := h.files[p] + commentFor(p) + fmt.Sprintf("rev %d\n", i)
		_ = writeFiles(h.dir, map[string]string{p: body})
		h.commit(demoMessages[h.rng.Intn(len(demoMessages))], demoAuthors[h.rng.Intn(len(demoAuthors))])
	}
	_ = writeFiles(h.dir, h.files)
	h.when = h.when.Add(20 * time.Hour)
	h.commit("Tidy up", demoAuthors[0])
}

// seedDemo adds a codebase to the owner's account (DEMO_DATA=true, for
// screenshots and trying the app): "Northwind Shop", a small shop with a Go
// API, a React storefront and a Python worker, scanned twice — before and
// after payments were added — so every page and the comparison have
// something to show. Runs once.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	key := fmt.Sprintf("seeded:%d:demo", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	c := &store.Codebase{Name: "Northwind Shop", Description: "Online shop: Go API, React storefront, Python worker for payment webhooks.", Source: "upload", Location: "northwind-shop.zip", Pinned: true}
	if err := s.store.CreateCodebase(ctx, owner, c); err != nil {
		return err
	}
	root := filepath.Join(s.workDir(c.ID), "src")
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	v1 := txtar("v1.txtar")
	if err := writeFiles(root, v1); err != nil {
		return err
	}
	h := &demoHistory{dir: root, rng: rand.New(rand.NewSource(42)), when: time.Now().AddDate(0, 0, -150).Truncate(time.Hour).Add(9 * time.Hour), files: v1}
	if _, err := exec.LookPath("git"); err == nil {
		h.ok = h.git(demoAuthors[0], h.when, "init", "-q", "-b", "main") == nil
	}
	h.commit("Northwind Shop: products, orders and the storefront", demoAuthors[0])
	h.churn(34, 80, []string{"internal/api/orders.go", "internal/store/orders.go", "internal/store/orders.go", "internal/api/router.go", "internal/api/auth.go",
		"web/src/pages/Cart.tsx", "web/src/api/client.ts", "web/src/pages/Products.tsx", "internal/store/products.go", "internal/api/products.go"})
	first, _, err := s.store.QueueScan(ctx, owner, c.ID, "new")
	if err != nil {
		return err
	}
	s.runScan(ctx, first.ID)

	v2 := txtar("v2.txtar")
	for p, b := range v2 {
		h.files[p] = b
	}
	if err := writeFiles(root, v2); err != nil {
		return err
	}
	h.when = h.when.Add(72 * time.Hour)
	h.commit("Payments, coupons, e-mails and the worker", demoAuthors[2])
	h.churn(42, 58, []string{"internal/billing/stripe.go", "internal/billing/stripe.go", "internal/store/orders.go", "internal/store/orders.go", "internal/api/payments.go",
		"internal/api/router.go", "worker/app/hooks.py", "worker/app/jobs.py", "web/src/pages/Cart.tsx", "internal/api/orders.go", "internal/store/coupons.go", "internal/billing/webhook.go"})
	second, _, err := s.store.QueueScan(ctx, owner, c.ID, "manual")
	if err != nil {
		return err
	}
	s.runScan(ctx, second.ID)

	notes := []store.Note{
		{CodebaseID: c.ID, Path: "internal/billing", Kind: "note", Text: "Stripe is still faked in Charge(); the real API call is the next step. Refunds only change the order status."},
		{CodebaseID: c.ID, Path: "internal/store/orders.go", Kind: "note", Text: "CreateOrder imports billing for discounts while billing imports store: move Discount into store to break the loop."},
		{CodebaseID: c.ID, Path: "", Kind: "ai", Model: "demo", ScanID: &second.ID, Text: "Northwind Shop is an online shop in three parts. `cmd/shop` starts a Go HTTP API (chi) whose routes live in `internal/api/router.go`: products, orders, coupons and a Stripe webhook under `/api/v1`, with staff-only routes behind `requireAdmin`. `internal/store` keeps customers, products, orders and coupons in SQLite (schema in `migrations/`). `internal/billing` charges and refunds orders and is used by both the API and the store — `store` and `billing` import each other, a loop worth breaking. `web/` is a React storefront that talks to the API through `src/api/client.ts`, and `worker/` is a small FastAPI service that receives payment webhooks and sends e-mails through a Redis queue.\n\nBefore changing things: `internal/store/orders.go` and `internal/billing/stripe.go` change most often, `STRIPE_WEBHOOK_SECRET` is read but not documented in `.env.example`, and `Charge` still returns a fake payment."},
	}
	for i := range notes {
		if err := s.store.SaveNote(ctx, owner, &notes[i]); err != nil {
			return err
		}
	}
	return s.store.CachePut(ctx, key, "1")
}
