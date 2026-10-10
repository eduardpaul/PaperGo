package dms

import (
	"context"
	"sort"

	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/resource"
)

type RevisionView struct {
	*ent.ItemRevision
	IsCurrent     bool     `json:"is_current"`
	ChangedFields []string `json:"changed_fields"`
}

func revisionChanges(current, previous *ent.ItemRevision) ([]string, error) {
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
		if exists != wasPresent || !smartEqual(value, before) {
			changed = append(changed, key)
		}
	}
	if previous == nil || current.Name != previous.Name {
		changed = append(changed, "$name")
	}
	if previous == nil || !smartEqual(current.Tags, previous.Tags) {
		changed = append(changed, "$tags")
	}
	if previous != nil && current.ContentTypeID != previous.ContentTypeID {
		changed = append(changed, "$content_type_id")
	}
	sort.Strings(changed)
	return changed, nil
}

func (s *Service) RevisionHistory(ctx context.Context, subject, id string, after, limit int) ([]RevisionView, error) {
	return read(ctx, s, func(t *Service) ([]RevisionView, error) {
		rows, err := t.Revisions(ctx, subject, id, after, limit)
		if err != nil {
			return nil, err
		}
		item, err := t.Client.Resource.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out := make([]RevisionView, 0, len(rows))
		for _, revision := range rows {
			entry, err := revision.Edges.HistoryOrErr()
			if err != nil {
				return nil, err
			}
			revision.Edges.History = nil
			out = append(out, RevisionView{revision, item.HeadRevisionID != nil && *item.HeadRevisionID == revision.ID, entry.ChangedFields})
		}
		return out, nil
	})
}

func (s *Service) Revision(ctx context.Context, subject, id string, number int) (RevisionView, error) {
	return read(ctx, s, func(t *Service) (RevisionView, error) {
		item, err := t.authorize(ctx, subject, id, "read")
		if err != nil {
			return RevisionView{}, err
		}
		if item.Kind != resource.KindItem || number < 1 {
			return RevisionView{}, ErrNotFound
		}
		v, err := t.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberEQ(number), itemrevision.HasHistory()).WithSchemaRevision().WithHistory().Only(ctx)
		if ent.IsNotFound(err) {
			return RevisionView{}, ErrNotFound
		}
		if err != nil {
			return RevisionView{}, err
		}
		entry, err := v.Edges.HistoryOrErr()
		if err != nil {
			return RevisionView{}, err
		}
		v.Edges.History = nil
		return RevisionView{v, item.HeadRevisionID != nil && *item.HeadRevisionID == v.ID, entry.ChangedFields}, nil
	})
}

// RestoreRevision restores metadata through the current mutation pipeline.
// File content is retained; restoring a blob is a separate content operation.
func (s *Service) RestoreRevision(ctx context.Context, subject, id string, number, version int) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		item, err := t.authorize(ctx, subject, id, "write")
		if err != nil {
			return err
		}
		if item.Kind != resource.KindItem || number < 1 {
			return ErrNotFound
		}
		if item.Version != version {
			return ErrConflict
		}
		old, err := t.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberEQ(number), itemrevision.HasHistory()).Only(ctx)
		if ent.IsNotFound(err) {
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
		collection, err := t.Client.Resource.Get(ctx, *item.ContainerID)
		if err != nil {
			return err
		}
		schema, err := t.Client.SchemaRevision.Get(ctx, *collection.SchemaHeadID)
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
		out, err = t.update(ctx, subject, id, version, UpdateResource{Name: &old.Name, Tags: &old.Tags, Values: &values, ContentTypeID: &old.ContentTypeID})
		if err != nil {
			return err
		}
		return t.audit(ctx, subject, "item.restore_revision", out, map[string]any{"restored_revision_number": number, "revision_id": *out.HeadRevisionID})
	})
	return
}
