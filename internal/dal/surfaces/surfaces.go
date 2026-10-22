// Package surfaces owns the projections readers query: the head and
// published surface of each item or folder, the typed field values indexed
// from a surface's payload, and the group counts that answer facet totals
// without reading every item. Drivers implement Queries; this package keeps
// the projections and counters consistent.
package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/taxonomy"
)

// Surface is the revision an item or folder shows on a surface, with the
// values readers filter and sort by.
type Surface struct {
	ID            string          `json:"id"`
	CreatedAt     time.Time       `json:"created_at"`
	ItemID        string          `json:"item_id"`
	ContainerID   string          `json:"container_id"`
	WorkspaceID   string          `json:"workspace_id"`
	Surface       refs.Surface    `json:"surface"`
	RevisionID    string          `json:"revision_id"`
	Name          string          `json:"name"`
	Keywords      []string        `json:"keywords"`
	Payload       json.RawMessage `json:"payload"`
	ItemCreatedAt time.Time       `json:"item_created_at"`
	ItemCreatedBy string          `json:"item_created_by"`
	ModifiedAt    time.Time       `json:"modified_at"`
	ModifiedBy    string          `json:"modified_by"`
}

// FieldValue is one indexed value of a surface's field. Exactly one value
// column is set; multi-valued fields number their values with Ordinal.
type FieldValue struct {
	ID           string
	SurfaceID    string
	ContainerID  string
	ItemID       string
	Surface      refs.Surface
	FieldKey     string
	FieldType    catalog.FieldType
	Scale        int64
	Ordinal      int64
	ValueText    *string
	ValueInteger *int64
	ValueNumeric *decimal.Decimal
	ValueBoolean *bool
	// AppliesParents makes a term value also apply the term's ancestors.
	AppliesParents bool
}

// GroupCount counts the live items of a collection's surface, all of them
// under the empty field key, or those whose single-choice field holds a
// value.
type GroupCount struct {
	ContainerID string
	Surface     refs.Surface
	FieldKey    string
	Value       string
	Count       int
}

// Filter selects surfaces ordered by item and surface. A nil list does not
// filter; an empty one selects nothing.
type Filter struct {
	IDs, ItemIDs, ContainerIDs       []string
	ItemID, ContainerID, WorkspaceID string
	Surface                          refs.Surface
	RevisionID                       string
	AfterItemID                      string
	Limit                            int
	// Fields keeps only these payload keys; nil keeps the whole payload.
	Fields []string
}

func (f Filter) empty() bool {
	return f.IDs != nil && len(f.IDs) == 0 || f.ItemIDs != nil && len(f.ItemIDs) == 0 || f.ContainerIDs != nil && len(f.ContainerIDs) == 0
}

// ValueFilter selects field values. Zero fields do not filter.
type ValueFilter struct {
	SurfaceID, ItemID, ContainerID, FieldKey string
	Surface                                  refs.Surface
	// ChoiceHeads selects the first value of choice fields, the one group
	// counts count.
	ChoiceHeads bool
}

// Queries are the statements a driver runs for surfaces. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	taxonomy.Queries
	Revision(ctx context.Context, id string) (revisions.Revision, error)
	// Surfaces leaves Payload empty when f.Fields is set.
	Surfaces(ctx context.Context, f Filter) ([]Surface, error)
	// SurfaceFields returns, by surface ID, an object of the keys each
	// payload holds.
	SurfaceFields(ctx context.Context, ids, keys []string) (map[string]json.RawMessage, error)
	InsertSurface(ctx context.Context, s Surface) error
	// UpdateSurface writes s's placement, revision, content and stamps.
	UpdateSurface(ctx context.Context, s Surface) error
	DeleteSurface(ctx context.Context, id string) error
	TouchSurfaces(ctx context.Context, itemIDs []string, at time.Time, actor string) error
	FieldValues(ctx context.Context, f ValueFilter) ([]FieldValue, error)
	// InsertFieldValues inserts values in as few statements as the engine's
	// parameter limit allows.
	InsertFieldValues(ctx context.Context, values []FieldValue) error
	DeleteFieldValues(ctx context.Context, f ValueFilter) error
	// AddGroupCount adds delta to a counter and drops counters that reach 0.
	AddGroupCount(ctx context.Context, c GroupCount, delta int) error
	// GroupCounts returns a surface's positive counters of the field keys.
	GroupCounts(ctx context.Context, containerID string, surface refs.Surface, keys []string) ([]GroupCount, error)
	// DeleteGroupCounts drops the counters of a collection's field.
	DeleteGroupCounts(ctx context.Context, containerID, fieldKey string) error
	// DropSurface deletes a collection's surfaces of one kind with their
	// values, keywords and counters.
	DropSurface(ctx context.Context, containerID string, surface refs.Surface) error
}

// Repo keeps surfaces, field values and group counts consistent for every
// engine. read runs single statements; write runs a function in one
// transaction, joining the caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Surfaces lists the surfaces f selects.
func (r *Repo) Surfaces(ctx context.Context, f Filter) ([]Surface, error) {
	if f.empty() {
		return []Surface{}, nil
	}
	rows, err := r.read.Surfaces(ctx, f)
	if err != nil || f.Fields == nil || len(rows) == 0 {
		return rows, err
	}
	ids := make([]string, len(rows))
	for i, s := range rows {
		ids[i] = s.ID
	}
	payloads := map[string]json.RawMessage{}
	if len(f.Fields) > 0 {
		if payloads, err = r.read.SurfaceFields(ctx, ids, f.Fields); err != nil {
			return nil, err
		}
	}
	for i := range rows {
		rows[i].Payload = payloads[rows[i].ID]
		if rows[i].Payload == nil {
			rows[i].Payload = json.RawMessage(`{}`)
		}
	}
	return rows, nil
}

// FieldValues lists the indexed values f selects, by field and ordinal.
func (r *Repo) FieldValues(ctx context.Context, f ValueFilter) ([]FieldValue, error) {
	return r.read.FieldValues(ctx, f)
}

// Exists reports whether f selects a surface.
func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	if f.empty() {
		return false, nil
	}
	f.Limit, f.Fields = 1, []string{}
	rows, err := r.read.Surfaces(ctx, f)
	return len(rows) > 0, err
}

// Projection is the revision an item shows on a surface, with the indexed
// values of its fields.
type Projection struct {
	ItemID     string
	Surface    refs.Surface
	RevisionID string
	// ModifiedAt and ModifiedBy default to the revision's creation.
	ModifiedAt time.Time
	ModifiedBy string
	Fields     []FieldValue
}

// Project shows a revision of a live item or folder on a surface, replacing
// what the surface showed, and keeps the collection's counts.
func (r *Repo) Project(ctx context.Context, p Projection) (Surface, error) {
	if !p.Surface.Valid() {
		return Surface{}, fmt.Errorf("%w: surface must be head or published", dberr.ErrCheckViolation)
	}
	var out Surface
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, p.ItemID); err != nil {
			return err
		}
		item, err := q.Resource(ctx, p.ItemID)
		if err != nil {
			return err
		}
		if item.Deleted {
			return fmt.Errorf("%w: deleted resources cannot be projected", dberr.ErrImmutable)
		}
		if item.Kind != refs.KindItem && item.Kind != refs.KindFolder {
			return fmt.Errorf("%w: surfaces only apply to items and folders", dberr.ErrInvalidContainment)
		}
		if item.ContainerID == "" {
			return fmt.Errorf("%w: the item is not in a collection", dberr.ErrInvalidContainment)
		}
		rev, err := q.Revision(ctx, p.RevisionID)
		if err != nil {
			return err
		}
		if rev.ItemID != p.ItemID {
			return fmt.Errorf("%w: the revision belongs to another item", dberr.ErrInvalidReference)
		}
		keywords, err := taxonomy.ResolveKeywords(ctx, q, rev.Keywords)
		if err != nil {
			return err
		}
		modifiedAt, modifiedBy := p.ModifiedAt.UTC(), p.ModifiedBy
		if p.ModifiedAt.IsZero() {
			modifiedAt = time.Now().UTC()
		}
		if modifiedBy == "" {
			modifiedBy = rev.CreatedBy
		}
		payload := rev.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		existing, err := q.Surfaces(ctx, Filter{ItemID: p.ItemID, Surface: p.Surface})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			out = existing[0]
			if err := choiceCounts(ctx, q, out, -1); err != nil {
				return err
			}
			if out.ContainerID != item.ContainerID {
				if err := q.AddGroupCount(ctx, GroupCount{ContainerID: out.ContainerID, Surface: out.Surface}, -1); err != nil {
					return err
				}
				if err := q.AddGroupCount(ctx, GroupCount{ContainerID: item.ContainerID, Surface: p.Surface}, 1); err != nil {
					return err
				}
			}
			out.ContainerID, out.WorkspaceID, out.RevisionID, out.Name = item.ContainerID, item.WorkspaceID, rev.ID, rev.Name
			out.Keywords, out.Payload, out.ModifiedAt, out.ModifiedBy = keywords, payload, modifiedAt, modifiedBy
			if err := q.UpdateSurface(ctx, out); err != nil {
				return err
			}
		} else {
			out = Surface{
				ID: uuid.NewString(), CreatedAt: time.Now().UTC(), ItemID: p.ItemID, ContainerID: item.ContainerID, WorkspaceID: item.WorkspaceID,
				Surface: p.Surface, RevisionID: rev.ID, Name: rev.Name, Keywords: keywords, Payload: payload,
				ItemCreatedAt: item.CreatedAt, ItemCreatedBy: item.CreatedBy, ModifiedAt: modifiedAt, ModifiedBy: modifiedBy,
			}
			if err := q.InsertSurface(ctx, out); err != nil {
				return err
			}
			if err := q.AddGroupCount(ctx, GroupCount{ContainerID: out.ContainerID, Surface: out.Surface}, 1); err != nil {
				return err
			}
		}
		if err := q.TagSurface(ctx, out.ID, keywords); err != nil {
			return err
		}
		return replaceValues(ctx, q, out, p.Fields)
	})
	return out, err
}

// Reindex replaces a surface's values of the field keys, or all its values
// when keys is nil.
func (r *Repo) Reindex(ctx context.Context, surfaceID string, keys []string, values []FieldValue) error {
	return r.write(ctx, func(q Queries) error {
		rows, err := q.Surfaces(ctx, Filter{IDs: []string{surfaceID}})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return dberr.ErrNotFound
		}
		s := rows[0]
		if err := q.LockWorkspace(ctx, s.ItemID); err != nil {
			return err
		}
		item, err := q.Resource(ctx, s.ItemID)
		if err != nil {
			return err
		}
		if item.Deleted {
			return fmt.Errorf("%w: deleted resources cannot be reindexed", dberr.ErrImmutable)
		}
		if keys != nil {
			replace := make(map[string]bool, len(keys))
			for _, key := range keys {
				replace[key] = true
			}
			for _, v := range values {
				if !replace[v.FieldKey] {
					return fmt.Errorf("%w: field is outside the reindex selection", dberr.ErrInvalidReference)
				}
			}
			kept, err := q.FieldValues(ctx, ValueFilter{SurfaceID: surfaceID})
			if err != nil {
				return err
			}
			for _, v := range kept {
				if !replace[v.FieldKey] {
					values = append(values, v)
				}
			}
		}
		if err := choiceCounts(ctx, q, s, -1); err != nil {
			return err
		}
		return replaceValues(ctx, q, s, values)
	})
}

// Remove deletes an item's surface, if it has one, and its counts.
func (r *Repo) Remove(ctx context.Context, itemID string, surface refs.Surface) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, itemID); err != nil {
			return err
		}
		item, err := q.Resource(ctx, itemID)
		if err != nil {
			return err
		}
		rows, err := q.Surfaces(ctx, Filter{ItemID: itemID, Surface: surface})
		if err != nil || len(rows) == 0 {
			return err
		}
		if !item.Deleted {
			if err := counts(ctx, q, rows[0], -1); err != nil {
				return err
			}
		}
		return q.DeleteSurface(ctx, rows[0].ID)
	})
}

// Touch stamps items' surfaces as modified without changing their content,
// as relationship changes do.
func (r *Repo) Touch(ctx context.Context, itemIDs []string, at time.Time, actor string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	return r.read.TouchSurfaces(ctx, itemIDs, at.UTC(), actor)
}

// RemoveField drops a collection's values and counts of a field.
func (r *Repo) RemoveField(ctx context.Context, containerID, key string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.DeleteFieldValues(ctx, ValueFilter{ContainerID: containerID, FieldKey: key}); err != nil {
			return err
		}
		return q.DeleteGroupCounts(ctx, containerID, key)
	})
}

// DropSurface deletes every surface of one kind in a collection, as a
// collection that stops publishing drops its heads.
func (r *Repo) DropSurface(ctx context.Context, containerID string, surface refs.Surface) error {
	return r.read.DropSurface(ctx, containerID, surface)
}

// Counts are a surface's item total and its counts by a choice field.
type Counts struct {
	Total  int
	Values []GroupCount
}

// Counts returns a collection surface's counts by a single-choice field.
func (r *Repo) Counts(ctx context.Context, containerID string, surface refs.Surface, key string) (Counts, error) {
	rows, err := r.read.GroupCounts(ctx, containerID, surface, []string{"", key})
	if err != nil {
		return Counts{}, err
	}
	out := Counts{Values: []GroupCount{}}
	for _, c := range rows {
		if c.FieldKey == "" {
			out.Total = c.Count
		} else {
			out.Values = append(out.Values, c)
		}
	}
	return out, nil
}

func replaceValues(ctx context.Context, q Queries, s Surface, values []FieldValue) error {
	if err := q.DeleteFieldValues(ctx, ValueFilter{SurfaceID: s.ID}); err != nil {
		return err
	}
	rows := make([]FieldValue, len(values))
	for i, v := range values {
		if v.ID == "" {
			v.ID = uuid.NewString()
		}
		v.SurfaceID, v.ContainerID, v.ItemID, v.Surface = s.ID, s.ContainerID, s.ItemID, s.Surface
		rows[i] = v
	}
	if err := q.InsertFieldValues(ctx, rows); err != nil {
		return err
	}
	for _, v := range rows {
		if counted(v) {
			if err := q.AddGroupCount(ctx, GroupCount{ContainerID: s.ContainerID, Surface: s.Surface, FieldKey: v.FieldKey, Value: *v.ValueText}, 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// counted reports whether a value is one group counts count: the first value
// of a choice field.
func counted(v FieldValue) bool {
	return v.FieldType == catalog.FieldChoice && v.Ordinal == 0 && v.ValueText != nil
}

// choiceCounts adds delta to the counts of a surface's choice values.
func choiceCounts(ctx context.Context, q Queries, s Surface, delta int) error {
	values, err := q.FieldValues(ctx, ValueFilter{SurfaceID: s.ID, ChoiceHeads: true})
	if err != nil {
		return err
	}
	for _, v := range values {
		if err := q.AddGroupCount(ctx, GroupCount{ContainerID: s.ContainerID, Surface: s.Surface, FieldKey: v.FieldKey, Value: *v.ValueText}, delta); err != nil {
			return err
		}
	}
	return nil
}

// counts adds delta to the counts a surface contributes to: its collection's
// total and its choice values.
func counts(ctx context.Context, q Queries, s Surface, delta int) error {
	if err := q.AddGroupCount(ctx, GroupCount{ContainerID: s.ContainerID, Surface: s.Surface}, delta); err != nil {
		return err
	}
	return choiceCounts(ctx, q, s, delta)
}

// CountItem adds or removes an item's surfaces from their collection's
// counts, as deleting and restoring it do. Surfaces outlive soft deletion;
// only live items are counted.
func CountItem(ctx context.Context, q Queries, itemID string, add bool) error {
	delta := -1
	if add {
		delta = 1
	}
	rows, err := q.Surfaces(ctx, Filter{ItemID: itemID})
	if err != nil {
		return err
	}
	for _, s := range rows {
		if err := counts(ctx, q, s, delta); err != nil {
			return err
		}
	}
	return nil
}

// Drop deletes an item's surfaces. The caller removes their counts first
// when the item is live.
func Drop(ctx context.Context, q Queries, itemID string) error {
	rows, err := q.Surfaces(ctx, Filter{ItemID: itemID, Fields: []string{}})
	if err != nil {
		return err
	}
	for _, s := range rows {
		if err := q.DeleteSurface(ctx, s.ID); err != nil {
			return err
		}
	}
	return nil
}
