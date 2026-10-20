package dms

import (
	"context"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"sort"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/revisions"
)

// RevisionView is a recorded version with the schema that interprets its
// values.
type RevisionView struct {
	*revisions.Revision
	IsCurrent      bool                    `json:"is_current"`
	ChangedFields  []string                `json:"changed_fields"`
	SchemaRevision *catalog.SchemaRevision `json:"schema_revision"`
}

func revisionChanges(current, previous *revisions.Revision) ([]string, error) {
	values, err := decodeValues(current.Payload)
	if err != nil {
		return nil, err
	}
	old := map[string]any{}
	if previous != nil {
		if old, err = decodeValues(previous.Payload); err != nil {
			return nil, err
		}
	}
	keys := map[string]bool{}
	for key := range values {
		keys[key] = true
	}
	for key := range old {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		value, exists := values[key]
		before, wasPresent := old[key]
		if exists != wasPresent || !sameJSON(value, before) {
			changed = append(changed, key)
		}
	}
	if previous == nil || current.Name != previous.Name {
		changed = append(changed, "$name")
	}
	if previous == nil || !sameJSON(current.Keywords, previous.Keywords) {
		changed = append(changed, "$keywords")
	}
	if previous != nil && current.ContentTypeID != previous.ContentTypeID {
		changed = append(changed, "$content_type_id")
	}
	sort.Strings(changed)
	return changed, nil
}

// historyViews shows history entries with the schemas of their revisions.
func (s *Service) historyViews(ctx context.Context, entries []revisions.Entry, current int) ([]RevisionView, error) {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.Revision.SchemaRevisionID
	}
	schemas, err := s.DAL.Catalog().Schemas(ctx, catalog.SchemaFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]*catalog.SchemaRevision{}
	for _, schema := range schemas {
		byID[schema.ID] = schema
	}
	out := make([]RevisionView, len(entries))
	for i, e := range entries {
		revision := e.Revision
		revision.RevisionNumber = e.Number
		out[i] = RevisionView{&revision, e.Number == current, e.ChangedFields, byID[revision.SchemaRevisionID]}
	}
	return out, nil
}
func (s *Service) RevisionHistory(ctx context.Context, subject, id string, after, limit int) (Page[RevisionView], error) {
	return read(ctx, s, func(t *Service) (Page[RevisionView], error) {
		item, err := t.authorize(ctx, subject, id, "read")
		if err != nil {
			return Page[RevisionView]{}, err
		}
		if item.Kind != refs.KindItem {
			return Page[RevisionView]{}, invalid("revisions belong to items")
		}
		if after < 0 {
			return Page[RevisionView]{}, invalid("history cursor must be a positive recorded version number")
		}
		entries, err := t.DAL.Revisions().Entries(ctx, revisions.HistoryFilter{ItemID: id, BeforeNumber: after, Limit: pageSize(limit) + 1})
		if err != nil {
			return Page[RevisionView]{}, err
		}
		current, err := t.DAL.Revisions().LastNumber(ctx, id)
		if err != nil {
			return Page[RevisionView]{}, err
		}
		out, err := t.historyViews(ctx, entries, current)
		if err != nil {
			return Page[RevisionView]{}, err
		}
		return numberedPage(out, limit, func(v RevisionView) int { return v.RevisionNumber }), nil
	})
}

func (s *Service) Revision(ctx context.Context, subject, id string, number int) (RevisionView, error) {
	return read(ctx, s, func(t *Service) (RevisionView, error) {
		item, err := t.authorize(ctx, subject, id, "read")
		if err != nil {
			return RevisionView{}, err
		}
		if item.Kind != refs.KindItem || number < 1 {
			return RevisionView{}, ErrNotFound
		}
		entries, err := t.DAL.Revisions().Entries(ctx, revisions.HistoryFilter{ItemID: id, Number: number})
		if err != nil {
			return RevisionView{}, err
		}
		if len(entries) == 0 {
			return RevisionView{}, ErrNotFound
		}
		current, err := t.DAL.Revisions().LastNumber(ctx, id)
		if err != nil {
			return RevisionView{}, err
		}
		views, err := t.historyViews(ctx, entries, current)
		if err != nil {
			return RevisionView{}, err
		}
		return views[0], nil
	})
}

// RestoreRevision restores metadata through the current mutation pipeline.
// File content is retained; restoring a blob is a separate content operation.
func (s *Service) RestoreRevision(ctx context.Context, subject, id string, number, version int) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		item, err := t.authorize(ctx, subject, id, "write")
		if err != nil {
			return err
		}
		if item.Kind != refs.KindItem || number < 1 {
			return ErrNotFound
		}
		if item.Version != version {
			return ErrPreconditionFailed
		}
		old, err := t.DAL.Revisions().Recorded(ctx, id, number)
		if dberr.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err = t.overlayHead(ctx, item); err != nil {
			return err
		}
		values, err := decodeValues(old.Payload)
		if err != nil {
			return err
		}
		collection, err := t.DAL.Resources().Resource(ctx, *item.ContainerID)
		if err != nil {
			return err
		}
		schema, err := t.DAL.Catalog().Schema(ctx, *collection.SchemaHeadID)
		if err != nil {
			return err
		}
		defs, _, err := typedDefinitions(schema.Definition, old.ContentTypeID)
		if err != nil {
			return err
		}
		// Removed fields stay removed. Clear current fields absent from the snapshot.
		known := definitionMap(defs)
		for key := range values {
			if known[key] == nil {
				delete(values, key)
			}
		}
		for key := range item.Values {
			if _, exists := values[key]; !exists && known[key] != nil {
				values[key] = nil
			}
		}
		if err = t.restoreIntegrationData(ctx, old.ID, id); err != nil {
			return err
		}
		out, err = t.update(ctx, subject, id, version, UpdateResource{Name: &old.Name, Keywords: &old.Keywords, Values: &values, ContentTypeID: &old.ContentTypeID})
		if err != nil {
			return err
		}
		return t.audit(ctx, subject, "item.restore_revision", out, map[string]any{"restored_revision_number": number, "revision_id": *out.HeadRevisionID})
	})
	return
}
