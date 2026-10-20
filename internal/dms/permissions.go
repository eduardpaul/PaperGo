package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"papergo/internal/identity"
)

const (
	MaxFolderDepth   = 64
	MaxResourceDepth = MaxFolderDepth + 3
)

// principal is the subject a request runs as, with its access token.
func principal(ctx context.Context, subject string) access.Principal {
	p := access.Principal{Subject: subject}
	if t, ok := identity.AccessTokenContext(ctx); ok {
		p.Credential = &access.Credential{ID: t.ID, Scopes: t.Scopes}
	}
	return p
}

func (s *Service) allowedMany(ctx context.Context, subject, action string, roots []*resources.Resource) (map[string]bool, error) {
	out := map[string]bool{}
	if len(roots) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(roots))
	for _, r := range roots {
		ids = append(ids, r.ID)
	}
	allowed, err := s.DAL.Resources().IDs(ctx, resources.Filter{IDs: ids, Reader: &resources.Reader{Principal: principal(ctx, subject), Action: access.Action(action)}})
	if err != nil {
		return nil, err
	}
	for _, id := range allowed {
		out[id] = true
	}
	return out, nil
}
func (s *Service) authorize(ctx context.Context, subject, id, action string) (*resources.Resource, error) {
	r, err := s.DAL.Resources().Resource(ctx, id)
	if dberr.IsNotFound(err) || err == nil && r.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.homeProvisioning {
		w, err := s.DAL.Resources().Resource(ctx, r.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if w.PersonalOwnerID != nil && *w.PersonalOwnerID == subject {
			return r, nil
		}
		return nil, ErrForbidden
	}
	allowed, err := s.allowedMany(ctx, subject, action, []*resources.Resource{r})
	if err != nil {
		return nil, err
	}
	if !allowed[id] {
		if r.Kind == refs.KindWorkspace {
			return nil, ErrNotFound
		}
		return nil, ErrForbidden
	}
	return r, nil
}

// headSurface is the surface holding a collection's head content.
func headSurface(c *resources.Resource) refs.Surface {
	if c.PublishingEnabled {
		return refs.SurfaceHead
	}
	return refs.SurfacePublished
}

func (s *Service) GetSurface(ctx context.Context, subject, id, surface string) (*resources.Resource, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*resources.Resource, error) { return t.GetSurface(ctx, subject, id, surface) })
	}
	if surface == "" {
		surface = "auto"
	}
	if surface != "auto" && surface != "head" && surface != "published" {
		return nil, invalid("surface must be auto, head or published")
	}
	r, err := s.authorize(ctx, subject, id, "read")
	if err != nil {
		return nil, err
	}
	if r.Kind == refs.KindFolder {
		if err = s.overlayHead(ctx, r); err != nil {
			return nil, err
		}
		return r, nil
	}
	if r.Kind != refs.KindItem {
		return r, nil
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*resources.Resource{r})
	if err != nil {
		return nil, err
	}
	if surface == "head" && !draft[id] {
		return nil, ErrForbidden
	}
	selected := r.PublishedRevisionID
	if surface == "head" || (surface == "auto" && draft[id]) {
		selected = r.HeadRevisionID
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	revision, err := s.revision(ctx, *selected)
	if err != nil {
		return nil, err
	}
	if err = overlayRevision(r, revision); err != nil {
		return nil, err
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemID: id, RevisionID: *selected, Fields: []string{}, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(projections) == 0 {
		return nil, ErrNotFound
	}
	r.UpdatedAt, r.UpdatedBy = projections[0].ModifiedAt, projections[0].ModifiedBy
	if !draft[id] || surface == "published" {
		r.HeadRevisionID = nil
		r.NextRevisionNumber = 0
	}
	return r, nil
}

// AccessActions lists the actions in the order the access field reports them.
var AccessActions = []string{"read", "read_draft", "write", "publish", "manage"}

// Access returns the actions the subject may take on r, so clients can offer
// only those. It applies the same ACL and module-scope rules as the writes:
// adding or changing content (write, publish) below a workspace also needs the
// list.write scope.
func (s *Service) Access(ctx context.Context, subject string, r *resources.Resource) ([]string, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]string, error) { return t.Access(ctx, subject, r) })
	}
	contentWrite := true
	if r.Kind != refs.KindWorkspace {
		if err := s.RequireScope(ctx, subject, "list.write"); errors.Is(err, ErrForbidden) {
			contentWrite = false
		} else if err != nil {
			return nil, err
		}
	}
	out := []string{}
	for _, action := range AccessActions {
		if (action == "write" || action == "publish") && !contentWrite {
			continue
		}
		allowed, err := s.allowedMany(ctx, subject, action, []*resources.Resource{r})
		if err != nil {
			return nil, err
		}
		if allowed[r.ID] {
			out = append(out, action)
		}
	}
	return out, nil
}
