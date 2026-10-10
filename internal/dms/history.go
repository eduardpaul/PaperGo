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
	sort.Strings(changed)
	return changed, nil
}

func (s *Service) previousRevision(ctx context.Context, id string, number int) (*ent.ItemRevision, error) {
	v, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberLT(number)).Order(ent.Desc(itemrevision.FieldRevisionNumber)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return v, err
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
		var previous *ent.ItemRevision
		if len(rows) > 0 {
			if previous, err = t.previousRevision(ctx, id, rows[0].RevisionNumber); err != nil {
				return nil, err
			}
		}
		for _, revision := range rows {
			changed, err := revisionChanges(revision, previous)
			if err != nil {
				return nil, err
			}
			out = append(out, RevisionView{revision, item.HeadRevisionID != nil && *item.HeadRevisionID == revision.ID, changed})
			previous = revision
		}
		return out, nil
	})
}

func (s *Service) Revision(ctx context.Context, subject, id string, number int) (RevisionView, error) {
	return read(ctx, s, func(t *Service) (RevisionView, error) {
		item, err := t.authorize(ctx, subject, id, "read_draft")
		if err != nil {
			return RevisionView{}, err
		}
		if item.Kind != resource.KindItem || number < 1 {
			return RevisionView{}, ErrNotFound
		}
		v, err := t.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberEQ(number)).WithSchemaRevision().Only(ctx)
		if ent.IsNotFound(err) {
			return RevisionView{}, ErrNotFound
		}
		if err != nil {
			return RevisionView{}, err
		}
		previous, err := t.previousRevision(ctx, id, number)
		if err != nil {
			return RevisionView{}, err
		}
		changed, err := revisionChanges(v, previous)
		return RevisionView{v, item.HeadRevisionID != nil && *item.HeadRevisionID == v.ID, changed}, err
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
		old, err := t.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberEQ(number)).Only(ctx)
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
		out, err = t.update(ctx, subject, id, version, UpdateResource{Name: &old.Name, Tags: &old.Tags, Values: &values})
		if err != nil {
			return err
		}
		return t.audit(ctx, subject, "item.restore_revision", out, map[string]any{"restored_revision_number": number, "revision_id": *out.HeadRevisionID})
	})
	return
}
