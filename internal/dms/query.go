package dms

import (
	"context"
	"maps"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	"slices"
	"strings"
)

type Page[T any] struct {
	Data       []T    `json:"value"`
	NextCursor string `json:"-"`
	// SearchCapped means the search text matched more entries than a
	// listing considers; narrow the words or filters for complete results.
	SearchCapped bool `json:"search_capped,omitempty"`
}
type Browse struct {
	Archive     refs.ArchiveSelection
	ParentID    string
	WorkspaceID string
	Search      string
	Keyword     string
	After       string
	Limit       int
	Surface     string
	FilterField string
	FilterOp    string
	FilterValue string
	// Kind keeps one kind of resource, e.g. the folders of a large library.
	Kind string
}

func pageSize(limit int) int {
	if limit < 1 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func (s *Service) Browse(ctx context.Context, subject string, in Browse) (Page[*resources.Resource], error) {
	if !s.transaction {
		if collection, workspace, ok := s.browseMatchScope(ctx, subject, in); ok {
			var err error
			if ctx, err = s.matchFirst(ctx, subject, collection, workspace, in.Search, in.Archive.Default()); err != nil {
				return Page[*resources.Resource]{}, err
			}
		}
		return read(ctx, s, func(t *Service) (Page[*resources.Resource], error) { return t.Browse(ctx, subject, in) })
	}
	scope := "list.read"
	if in.WorkspaceID == "" && in.ParentID == "" {
		scope = "workspace.read"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return Page[*resources.Resource]{}, err
	}
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return Page[*resources.Resource]{}, invalid("surface must be auto, head or published")
	}
	if len(in.Search) > 256 {
		return Page[*resources.Resource]{}, invalid("search is limited to 256 bytes")
	}
	if in.Kind != "" && !refs.ResourceKind(in.Kind).Valid() {
		return Page[*resources.Resource]{}, invalid("kind must be workspace, list, library, folder or item")
	}
	if in.Keyword != "" {
		var err error
		in.Keyword, err = s.CanonicalKeyword(ctx, in.Keyword)
		if err != nil {
			return Page[*resources.Resource]{}, err
		}
	}
	if !in.Archive.Valid() {
		return Page[*resources.Resource]{}, invalid("archive must be active, archived or all")
	}
	b := content.Browse{Archive: in.Archive.Default(), Reader: principal(ctx, subject), Surface: in.Surface, Kind: refs.ResourceKind(in.Kind), Keyword: in.Keyword, Limit: pageSize(in.Limit) + 1}
	// Search text matches items and folders through the text matcher, within
	// the parent's collection or the workspace, and other resources by name.
	var matchCollection, matchWorkspace string
	if in.ParentID != "" {
		parent, err := s.authorize(ctx, subject, in.ParentID, "read")
		if err != nil {
			return Page[*resources.Resource]{}, err
		}
		b.ParentID = in.ParentID
		switch {
		case parent.Kind == refs.KindList || parent.Kind == refs.KindLibrary:
			matchCollection = parent.ID
		case parent.ContainerID != nil:
			matchCollection = *parent.ContainerID
		default:
			matchWorkspace = parent.WorkspaceID
		}
	} else if in.WorkspaceID != "" {
		matchWorkspace = in.WorkspaceID
		w, err := s.authorize(ctx, subject, in.WorkspaceID, "read")
		if err != nil {
			return Page[*resources.Resource]{}, err
		}
		if w.Kind != refs.KindWorkspace {
			return Page[*resources.Resource]{}, invalid("workspace_id must reference a workspace")
		}
		b.WorkspaceID = in.WorkspaceID
	} else if in.Search != "" || in.Keyword != "" {
		return Page[*resources.Resource]{}, invalid("search and keywords require workspace_id or parent_id")
	}
	if in.FilterField != "" {
		field, err := s.fieldFilter(ctx, subject, in)
		if err != nil {
			return Page[*resources.Resource]{}, err
		}
		b.Field = field
	}
	capped := false
	if strings.TrimSpace(in.Search) != "" {
		var err error
		if b.Search, capped, err = s.matchText(ctx, subject, matchCollection, matchWorkspace, in.Search, in.Archive.Default()); err != nil {
			return Page[*resources.Resource]{}, err
		}
	}
	normalized := in
	normalized.After, normalized.Limit = "", 0
	normalized.Archive = in.Archive.Default()
	fingerprint := queryFingerprint(struct {
		Subject string
		Query   Browse
	}{subject, normalized})
	if in.After != "" {
		id, ok := decodePageCursor[string](in.After, "b1.", fingerprint)
		if !ok || id == "" {
			return Page[*resources.Resource]{}, invalid("browse cursor does not match query or caller")
		}
		b.AfterID = id
	}
	ids, err := s.DAL.Content().BrowseIDs(ctx, b)
	if err != nil {
		return Page[*resources.Resource]{}, err
	}
	rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids})
	if err != nil {
		return Page[*resources.Resource]{}, err
	}
	out := entityPage(rows, in.Limit, func(v *resources.Resource) string {
		return encodePageCursor("b1.", fingerprint, v.ID)
	})
	out.SearchCapped = capped
	if _, err = s.overlayPage(ctx, subject, in.Surface, out.Data, nil); err != nil {
		return out, err
	}
	return out, nil
}

// browseMatchScope is where a listing's search text matches, before its
// read transaction: in the parent's collection, or across the workspace.
// It reports false when there is no text or the listing will fail anyway.
func (s *Service) browseMatchScope(ctx context.Context, subject string, in Browse) (collection, workspace string, ok bool) {
	if strings.TrimSpace(in.Search) == "" {
		return "", "", false
	}
	if in.ParentID == "" {
		return "", in.WorkspaceID, in.WorkspaceID != ""
	}
	parent, err := s.Authorize(ctx, subject, in.ParentID, "read")
	switch {
	case err != nil:
		return "", "", false
	case parent.Kind == refs.KindList || parent.Kind == refs.KindLibrary:
		return parent.ID, "", true
	case parent.ContainerID != nil:
		return *parent.ContainerID, "", true
	default:
		return "", parent.WorkspaceID, true
	}
}

// overlayPage applies each item's selected surface and returns the selected revision
// of every item, keyed by item ID, with its creation time and blob. Only the
// selected surface row is read. A non-nil fields limits Values to those keys,
// extracted in SQL, so list rows never decode payloads they discard.
func (s *Service) overlayPage(ctx context.Context, subject, surface string, rows []*resources.Resource, fields []string) (map[string]*revisions.Revision, error) {
	ids := []string{}
	items := []*resources.Resource{}
	for _, r := range rows {
		if contentKind(r.Kind) {
			ids = append(ids, r.ID)
			items = append(items, r)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", items)
	if err != nil {
		return nil, err
	}
	// Items of collections without publishing have one surface, the published one.
	containers := map[string]bool{}
	for _, r := range items {
		if r.ContainerID != nil {
			containers[*r.ContainerID] = true
		}
	}
	explicit := true
	publishing, err := s.DAL.Resources().IDs(ctx, resources.Filter{IDs: slices.Collect(maps.Keys(containers)), Publishing: &explicit})
	if err != nil {
		return nil, err
	}
	publishes := map[string]bool{}
	for _, id := range publishing {
		publishes[id] = true
	}
	wantsHead := func(r *resources.Resource) bool { return surface == "head" || (surface == "auto" && draft[r.ID]) }
	selectedSurface := func(r *resources.Resource) refs.Surface {
		if r.ContainerID != nil && publishes[*r.ContainerID] && wantsHead(r) {
			return refs.SurfaceHead
		}
		return refs.SurfacePublished
	}
	bySurface := map[refs.Surface][]string{}
	for _, r := range items {
		bySurface[selectedSurface(r)] = append(bySurface[selectedSurface(r)], r.ID)
	}
	lookup := map[string]surfaces.Surface{}
	revisionIDs := []string{}
	for sf, members := range bySurface {
		projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{Surface: sf, ItemIDs: members, Fields: fields})
		if err != nil {
			return nil, err
		}
		for _, p := range projections {
			lookup[p.ItemID] = p
			revisionIDs = append(revisionIDs, p.RevisionID)
		}
	}
	revisionLookup, err := s.revisionsByID(ctx, revisionIDs)
	if err != nil {
		return nil, err
	}
	selectedRevisions := make(map[string]*revisions.Revision, len(items))
	for _, r := range items {
		selected := selectedSurface(r)
		p, ok := lookup[r.ID]
		if !ok {
			return nil, ErrNotFound
		}
		values, err := decodeValues(p.Payload)
		if err != nil {
			return nil, err
		}
		r.Name = p.Name
		r.Keywords = p.Keywords
		r.Values = values
		r.UpdatedBy = p.ModifiedBy
		if rev := revisionLookup[p.RevisionID]; rev != nil {
			r.UpdatedAt = p.ModifiedAt
			selectedRevisions[r.ID] = rev
		}
		// Readers of published content do not learn about drafts.
		if selected == refs.SurfacePublished && !wantsHead(r) {
			r.HeadRevisionID = nil
			r.NextRevisionNumber = 0
		}
	}
	return selectedRevisions, nil
}
