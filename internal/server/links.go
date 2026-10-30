package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Blueprint as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).
//
// Pocket AI and Pocket Projects read what a scan found (the structure, the
// endpoints, who calls what) instead of reading a whole repository, and may
// add a repository and start a scan (blueprint:write).

var linkOffers = []core.Offer{
	{Scope: "blueprint:read", Name: "See your codebases", Description: "See the codebases you scanned and what the scans found: folders and files, which file uses which, functions and types, API endpoints and the OpenAPI document, database tables, environment variables and the files that change most. Never your git tokens."},
	{Scope: "blueprint:write", Name: "Scan code for you", Description: "Add a git repository as a codebase and start a new scan of a codebase (Pocket Projects scanning a team's repository, Pocket AI looking at fresh code). It cannot delete anything."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(codebases|search)$`), "blueprint:read blueprint:write"},
	{"GET", regexp.MustCompile(`^/api/v1/codebases/\d+(/scans)?$`), "blueprint:read blueprint:write"},
	{"GET", regexp.MustCompile(`^/api/v1/scans/\d+(/(overview|tree|file|graph|symbols|symbol|endpoints|openapi\.json|openapi\.yaml|data|config|hotspots|report\.md|compare/\d+))?$`), "blueprint:read"},
	{"POST", regexp.MustCompile(`^/api/v1/codebases$`), "blueprint:write"},
	{"POST", regexp.MustCompile(`^/api/v1/codebases/\d+/scan$`), "blueprint:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			for _, need := range strings.Fields(lr.scope) {
				if sc == need {
					return true
				}
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "blueprint" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
