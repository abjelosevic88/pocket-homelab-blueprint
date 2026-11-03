package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// codebaseFor is a codebase the person asking may read: their own, or one
// shared with them (Role tells which). Apps linked through Pocket Core reach
// only the person's own codebases.
func (s *Server) codebaseFor(ctx context.Context, id int64) (*store.Codebase, error) {
	if linkApp(ctx) != "" {
		return s.store.GetCodebase(ctx, actorID(ctx), id)
	}
	return s.store.SharedCodebase(ctx, actorID(ctx), id)
}

// listCodebases is the person's codebases and those shared with them (for
// linked apps, their own only).
func (s *Server) listCodebases(ctx context.Context) ([]store.Codebase, error) {
	if linkApp(ctx) != "" {
		return s.store.OwnCodebases(ctx, actorID(ctx))
	}
	return s.store.ListCodebases(ctx, actorID(ctx))
}

// scanAccess is a scan with its codebase, when the person may read it.
// Scans are stored under the codebase's owner, so read them with c.UserID.
func (s *Server) scanAccess(ctx context.Context, id int64) (*store.Scan, *store.Codebase, error) {
	sc, err := s.store.ScanByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	c, err := s.codebaseFor(ctx, sc.CodebaseID)
	if err != nil {
		return nil, nil, store.ErrNotFound
	}
	return sc, c, nil
}

// mayScan tells whether the person may scan a codebase again.
func mayScan(c *store.Codebase) bool { return c.Role == "owner" || c.Role == store.ShareScan }

// handleListShares is who a codebase is shared with (its owner asks).
func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if _, err := s.store.GetCodebase(ctx, actorID(ctx), id); err != nil {
		s.fail(w, err, "codebase")
		return
	}
	list, err := s.store.ListShares(ctx, id)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handlePutShare shares a codebase with someone on this box, or changes
// what they may do: {"role": "view" | "scan"}.
func (s *Server) handlePutShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "only the owner shares a codebase")
		return
	}
	id, to := idParam(r, "id"), idParam(r, "uid")
	c, err := s.store.GetCodebase(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "codebase")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil || (in.Role != store.ShareView && in.Role != store.ShareScan) {
		writeErr(w, http.StatusBadRequest, `role is "view" or "scan"`)
		return
	}
	if to == uid {
		writeErr(w, http.StatusBadRequest, "it is already yours")
		return
	}
	who, err := s.store.GetUser(ctx, to)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "no such person here")
		return
	}
	created, err := s.store.SetShare(ctx, id, to, in.Role)
	if err != nil {
		s.fail(w, err, "share")
		return
	}
	if created {
		me := userFrom(ctx)
		link := ""
		if s.cfg.BaseURL != "" {
			link = fmt.Sprintf("%s/c/%d", s.cfg.BaseURL, c.ID)
		}
		s.publish(ctx, to, "blueprint.shared", fmt.Sprintf("codebase:%d", c.ID), map[string]any{"codebase_id": c.ID, "name": c.Name, "role": in.Role,
			"by": store.DisplayName(me.Name, me.Email), "url": link})
	}
	writeJSON(w, http.StatusOK, store.Share{UserID: who.ID, Name: who.Name, Email: who.Email, Role: in.Role})
}

// handleDeleteShare stops sharing with someone. The owner removes anyone;
// a person may also leave a codebase shared with them.
func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id, to := idParam(r, "id"), idParam(r, "uid")
	if to != uid {
		if _, err := s.store.GetCodebase(ctx, uid, id); err != nil || linkApp(ctx) != "" {
			s.fail(w, store.ErrNotFound, "codebase")
			return
		}
	}
	if err := s.store.RemoveShare(ctx, id, to); err != nil {
		s.fail(w, err, "share")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePeople is everyone on this box one may share with.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "not for apps")
		return
	}
	list, err := s.store.People(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
