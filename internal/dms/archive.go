package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"strings"
	"time"
)

// SetArchived changes an item's discovery state with its existing write permission.
func (s *Service) SetArchived(ctx context.Context, subject, id string, version int, archived bool) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "list.write"); e != nil {
			return e
		}
		r, e := t.authorize(ctx, subject, id, "write")
		if e != nil {
			return e
		}
		if r.Kind != refs.KindItem {
			return invalid("only documents and list items can be archived")
		}
		if r.Version != version {
			return ErrPreconditionFailed
		}
		changed, e := t.DAL.Resources().SetArchived(ctx, id, version, subject, archived)
		if e != nil {
			return e
		}
		if changed {
			typ, action := EventItemUnarchived, "item.unarchive"
			if archived {
				typ, action = EventItemArchived, "item.archive"
			}
			if e = t.audit(ctx, subject, action, r, map[string]any{"archived": archived}); e != nil {
				return e
			}
			t.emit(ctx, typ, subject, r.WorkspaceID, r.ContainerID, id, map[string]any{"archived": archived})
		}
		out, e = t.Get(ctx, subject, id)
		return e
	})
	return
}

// ArchiveQuery lists archived items across accessible collections without a search index.
type ArchiveQuery struct {
	WorkspaceID   string
	CollectionID  string
	ContentTypeID string
	Search        string
	Keyword       string
	ArchivedBy    string
	From, To      time.Time
	After         string
	Limit         int
}

type archiveCursor struct {
	Fingerprint string    `json:"query"`
	At          time.Time `json:"at"`
	ID          string    `json:"id"`
}

func (s *Service) archiveItems(ctx context.Context, subject string, in ArchiveQuery) (Page[*resources.Resource], error) {
	return read(ctx, s, func(t *Service) (Page[*resources.Resource], error) {
		out := Page[*resources.Resource]{Data: []*resources.Resource{}}
		if err := t.RequireScope(ctx, subject, "list.read"); err != nil {
			return out, err
		}
		if len(in.Search) > 256 {
			return out, invalid("search is limited to 256 bytes")
		}
		if !in.From.IsZero() && !in.To.IsZero() && in.From.After(in.To) {
			return out, invalid("archived_from must not follow archived_to")
		}
		keyword, err := t.CanonicalKeyword(ctx, in.Keyword)
		if err != nil {
			return out, err
		}
		in.Keyword = keyword
		normalized := in
		normalized.After, normalized.Limit = "", 0
		raw, _ := json.Marshal(struct {
			Subject string
			Query   ArchiveQuery
		}{subject, normalized})
		sum := sha256.Sum256(raw)
		fingerprint := hex.EncodeToString(sum[:])
		b := content.Browse{Reader: principal(ctx, subject), Surface: "auto", GlobalItems: true, WorkspaceID: in.WorkspaceID, CollectionID: in.CollectionID, ContentTypeID: in.ContentTypeID, Archive: refs.ArchiveArchived, Keyword: keyword, ArchivedBy: in.ArchivedBy, ArchivedFrom: in.From, ArchivedTo: in.To, Limit: pageSize(in.Limit) + 1}
		if strings.TrimSpace(in.Search) != "" {
			b.Search = &content.SearchMatch{ByName: true, Name: refs.FoldName(strings.TrimSpace(in.Search))}
		}
		if len(in.After) > 4096 {
			return out, invalid("invalid archive cursor")
		}
		if in.After != "" {
			var c archiveCursor
			raw, e := base64.RawURLEncoding.DecodeString(in.After)
			if e != nil || json.Unmarshal(raw, &c) != nil || c.Fingerprint != fingerprint || c.ID == "" || c.At.IsZero() {
				return out, invalid("archive cursor does not match this query or caller")
			}
			b.BeforeArchivedAt, b.BeforeArchivedID = c.At, c.ID
		}
		ids, err := t.DAL.Content().BrowseIDs(ctx, b)
		if err != nil {
			return out, err
		}
		rows, err := t.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids})
		if err != nil {
			return out, err
		}
		byID := map[string]*resources.Resource{}
		for _, r := range rows {
			byID[r.ID] = r
		}
		for _, id := range ids {
			if r := byID[id]; r != nil {
				out.Data = append(out.Data, r)
			}
		}
		if len(out.Data) > pageSize(in.Limit) {
			out.Data = out.Data[:pageSize(in.Limit)]
			last := out.Data[len(out.Data)-1]
			raw, _ = json.Marshal(archiveCursor{fingerprint, *last.ArchivedAt, last.ID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}
		_, err = t.overlayPage(ctx, subject, "auto", out.Data, nil)
		return out, err
	})
}

// ArchiveEntry includes the caller's write access, calculated in one batch.
type ArchiveEntry struct {
	*resources.Resource
	CanWrite bool `json:"can_write"`
}

func (s *Service) ArchiveItems(ctx context.Context, subject string, in ArchiveQuery) (Page[ArchiveEntry], error) {
	return read(ctx, s, func(t *Service) (Page[ArchiveEntry], error) {
		page, err := t.archiveItems(ctx, subject, in)
		out := Page[ArchiveEntry]{Data: []ArchiveEntry{}, NextCursor: page.NextCursor}
		if err != nil {
			return out, err
		}
		writable := map[string]bool{}
		if e := t.RequireScope(ctx, subject, "list.write"); e == nil {
			writable, err = t.allowedMany(ctx, subject, "write", page.Data)
		} else if !errors.Is(e, ErrForbidden) {
			return out, e
		}
		if err != nil {
			return out, err
		}
		for _, r := range page.Data {
			out.Data = append(out.Data, ArchiveEntry{r, writable[r.ID]})
		}
		return out, nil
	})
}
