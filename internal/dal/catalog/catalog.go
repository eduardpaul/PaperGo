// Package catalog owns the organization's field definitions and content
// types, and the content types each collection accepts. Drivers implement
// Queries; this package decides what those statements mean.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

// MaxCollectionTypes bounds the content types a collection accepts.
const MaxCollectionTypes = 32

// MaxFields bounds the fields a content type has with its ancestors, and
// those a collection resolves.
const MaxFields = 200

// MaxDepth bounds the ancestors of a content type. Catalog types have at
// most MaxDepth-1, so that each has room for the list types deriving from it.
const MaxDepth = 8

// Violation is a catalog rule a change breaks; its text explains it.
type Violation string

func (v Violation) Error() string { return string(v) }

// Errors a detach reports when the collection still needs the type.
var (
	ErrLastContentType  = Violation("a collection keeps at least one content type")
	ErrContentTypeInUse = Violation("live items of the collection use the content type")
)

// FieldDefinition is an organization field. Key, type, scale and the
// cardinality and reference options never change.
type FieldDefinition struct {
	ID          string             `json:"id,omitempty"`
	CreatedAt   time.Time          `json:"created_at,omitempty"`
	Key         string             `json:"key,omitempty"`
	Label       string             `json:"label,omitempty"`
	Type        FieldType          `json:"type,omitempty"`
	Required    bool               `json:"required,omitempty"`
	Choices     []string           `json:"choices,omitempty"`
	Indexed     bool               `json:"indexed,omitempty"`
	Version     int                `json:"version,omitempty"`
	IndexStatus IndexStatus        `json:"index_status,omitempty"`
	Scale       int                `json:"scale,omitempty"`
	Options     model.FieldOptions `json:"options,omitempty"`
}

// ContentType is a content type: the fields its items carry and the rules
// their values follow. A catalog type belongs to the organization. A list type
// is the copy of one catalog type that a list or library customizes; it keeps
// that type's key and name. Every type but a root derives from a parent and
// has every field and rule of its ancestors; Effective holds them all.
type ContentType struct {
	ID        string    `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Key       string    `json:"key,omitempty"`
	Name      string    `json:"name,omitempty"`
	// Group files a catalog type under a heading of the catalog, such as
	// Finance; list types have none.
	Group       string  `json:"group,omitempty"`
	TemplateKey *string `json:"template_key,omitempty"`
	// ParentID is the type this one derives from; it never changes.
	ParentID *string `json:"parent_id,omitempty"`
	// CollectionID is the list or library of a list type.
	CollectionID *string `json:"collection_id,omitempty"`
	// IsDefault marks the list type a collection's new items get.
	IsDefault bool `json:"is_default,omitempty"`
	// Attached is false once the collection stops accepting a list type.
	Attached bool `json:"-"`
	// InNewMenu offers a list type when people create items, in
	// MenuPosition order; the default always is.
	InNewMenu    bool `json:"in_new_menu"`
	MenuPosition int  `json:"-"`
	// FieldIds, HiddenFieldIds and Rules are the type's own. Hidden fields
	// are inherited fields that forms leave out.
	FieldIds       []string               `json:"field_ids"`
	HiddenFieldIds []string               `json:"hidden_field_ids"`
	Rules          []model.ValidationRule `json:"rules"`
	// FieldOrder orders fields of the type, own or inherited, in forms;
	// fields it leaves out follow in the order the type inherits.
	FieldOrder []string `json:"field_order"`
	// FieldSettings change, for the type and its subtypes, whether a field
	// is required and its default.
	FieldSettings []FieldSetting `json:"field_settings"`
	// AncestorIDs lists the ancestors, nearest first.
	AncestorIDs []string  `json:"ancestor_ids"`
	Effective   Effective `json:"effective"`
	Version     int       `json:"version,omitempty"`
}

// Effective is what a type and its ancestors define together, the root's
// first. FieldIds are in form order, and FieldSettings merge every setting of
// the lineage, the nearest type's winning.
type Effective struct {
	FieldIds       []string               `json:"field_ids"`
	HiddenFieldIds []string               `json:"hidden_field_ids"`
	Rules          []model.ValidationRule `json:"rules"`
	FieldSettings  []FieldSetting         `json:"field_settings"`
}

// FieldSetting changes, for a type and its subtypes, a field's Required flag
// and its DefaultValue; nil keeps what the field or an ancestor says, and a
// JSON null DefaultValue removes the default.
type FieldSetting struct {
	FieldID      string          `json:"field_id"`
	Required     *bool           `json:"required,omitempty"`
	DefaultValue json.RawMessage `json:"default_value,omitempty"`
}

// Setting returns the setting of a field, or a zero one.
func (e Effective) Setting(fieldID string) FieldSetting {
	for _, s := range e.FieldSettings {
		if s.FieldID == fieldID {
			return s
		}
	}
	return FieldSetting{FieldID: fieldID}
}

// Catalog reports whether t is a catalog type rather than a list type.
func (t *ContentType) Catalog() bool { return t.CollectionID == nil }

// FieldFilter selects fields ordered by key. A nil list does not filter;
// an empty one selects nothing. CollectionID selects the fields of the types
// a collection accepts.
type FieldFilter struct {
	IDs, Keys    []string
	Types        []FieldType
	CollectionID string
	ExcludeType  FieldType
}

// ContentTypeFilter selects content types ordered by key. A nil IDs does
// not filter; an empty one selects nothing.
type ContentTypeFilter struct {
	IDs []string
	// FieldID selects the types that own a field.
	TemplateKey, FieldID, ParentID string
	// Catalog selects catalog types.
	Catalog bool
	// CollectionID selects the list types a collection accepts, and
	// CollectionIDs those any of the collections accept; an empty
	// CollectionIDs selects nothing. Detached also selects the list types
	// the collections no longer accept.
	CollectionID  string
	CollectionIDs []string
	Detached      bool
}

// Queries are the statements a driver runs for the catalog. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	SchemaQueries
	KeyQueries
	FieldDefinitions(ctx context.Context, f FieldFilter) ([]*FieldDefinition, error)
	FieldDefinition(ctx context.Context, id string) (*FieldDefinition, error)
	InsertFieldDefinition(ctx context.Context, d *FieldDefinition) error
	// UpdateFieldDefinition writes d's label, flags, choices and options when
	// its version still is d.Version-1.
	UpdateFieldDefinition(ctx context.Context, d *FieldDefinition) (bool, error)
	DeleteFieldDefinition(ctx context.Context, id string) error
	SetFieldIndexStatus(ctx context.Context, ids []string, status IndexStatus) (int, error)
	// ContentTypes and ContentType read stored types; the repository adds
	// their lineage and effective schema.
	ContentTypes(ctx context.Context, f ContentTypeFilter) ([]*ContentType, error)
	ContentType(ctx context.Context, id string) (*ContentType, error)
	// InsertContentType adds t and its lineage from t.AncestorIDs.
	InsertContentType(ctx context.Context, t *ContentType) error
	// InsertLineage records the ancestors of a stored type, nearest first.
	InsertLineage(ctx context.Context, id string, ancestorIDs []string) error
	// UpdateContentType writes t's name, fields, hidden fields and rules when
	// its version still is t.Version-1.
	UpdateContentType(ctx context.Context, t *ContentType) (bool, error)
	// BumpContentTypes advances the versions of content types.
	BumpContentTypes(ctx context.Context, ids []string, at time.Time) error
	// Lineages lists the ancestors of each type, nearest first.
	Lineages(ctx context.Context, ids []string) (map[string][]string, error)
	// Descendants lists the types and every type deriving from them.
	Descendants(ctx context.Context, ids []string) ([]string, error)
	// SetAttached records whether a collection accepts a list type; a
	// detached type is no default.
	SetAttached(ctx context.Context, id string, attached bool) error
	// SetNewMenu offers a list type in the New menu at a position, or not.
	SetNewMenu(ctx context.Context, id string, offered bool, position int) error
	// SetDefaultContentType makes a list type its collection's default,
	// which the New menu offers.
	SetDefaultContentType(ctx context.Context, collectionID, id string) error
	// AttachedCollections lists the collections that accept a type deriving
	// from any of the types.
	AttachedCollections(ctx context.Context, contentTypeIDs []string) ([]string, error)
	// ContentTypeInUse reports whether live resources of the collection have
	// the content type.
	ContentTypeInUse(ctx context.Context, collectionID, contentTypeID string) (bool, error)
	// DeleteUnusedListTypes deletes the list types of a collection that no
	// resource or revision has.
	DeleteUnusedListTypes(ctx context.Context, collectionID string) error
	// DeleteContentType deletes a type and its lineage.
	DeleteContentType(ctx context.Context, id string) error
}

// Repo applies the catalog's rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Fields selects fields; a collection has every field of the types it
// accepts.
func (r *Repo) Fields(ctx context.Context, f FieldFilter) ([]*FieldDefinition, error) {
	if f.CollectionID != "" {
		types, err := r.ContentTypes(ctx, ContentTypeFilter{CollectionID: f.CollectionID})
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, t := range types {
			for _, id := range t.Effective.FieldIds {
				if f.IDs == nil || slices.Contains(f.IDs, id) {
					ids = append(ids, id)
				}
			}
		}
		f.IDs, f.CollectionID = ids, ""
	}
	return r.read.FieldDefinitions(ctx, f)
}

// Field returns a field, or dberr.ErrNotFound.
func (r *Repo) Field(ctx context.Context, id string) (*FieldDefinition, error) {
	return r.read.FieldDefinition(ctx, id)
}

// OneField returns the only field f selects: dberr.ErrNotFound when none
// does, dberr.ErrUniqueViolation when several do.
func (r *Repo) OneField(ctx context.Context, f FieldFilter) (*FieldDefinition, error) {
	return one(r.Fields(ctx, f))
}

func (r *Repo) FieldExists(ctx context.Context, f FieldFilter) (bool, error) {
	rows, err := r.Fields(ctx, f)
	return len(rows) > 0, err
}

func (r *Repo) CountFields(ctx context.Context, f FieldFilter) (int, error) {
	rows, err := r.Fields(ctx, f)
	return len(rows), err
}

// CreateField adds a field whose type, scale and options agree.
func (r *Repo) CreateField(ctx context.Context, d FieldDefinition) (*FieldDefinition, error) {
	if err := validateField(d); err != nil {
		return nil, err
	}
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Choices == nil {
		d.Choices = []string{}
	}
	d.Version, d.IndexStatus, d.CreatedAt = 1, IndexReady, time.Now().UTC()
	return &d, r.write(ctx, func(q Queries) error { return q.InsertFieldDefinition(ctx, &d) })
}

func validateField(d FieldDefinition) error {
	if !d.Type.Valid() {
		return fmt.Errorf("%w: invalid field type %q", dberr.ErrCheckViolation, d.Type)
	}
	if d.Scale < 0 || d.Scale > 9 || d.Type != FieldDecimal && d.Scale != 0 {
		return fmt.Errorf("%w: invalid field scale %d for type %q", dberr.ErrCheckViolation, d.Scale, d.Type)
	}
	if d.Options.Multiple && !d.Type.Multivalued() {
		return fmt.Errorf("%w: multiple cardinality not supported for field type %q", dberr.ErrCheckViolation, d.Type)
	}
	if d.Options.ApplyParents && !d.Type.Terms() {
		return fmt.Errorf("%w: parent terms apply only to term and keywords fields, not %q", dberr.ErrCheckViolation, d.Type)
	}
	return nil
}

// SaveField changes a field's label, flags, choices and options at
// version. Cardinality, reference scope and parent application never change.
func (r *Repo) SaveField(ctx context.Context, in FieldDefinition, version int) (*FieldDefinition, error) {
	var out *FieldDefinition
	err := r.write(ctx, func(q Queries) error {
		d, err := q.FieldDefinition(ctx, in.ID)
		if err != nil {
			return err
		}
		if d.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		old, next := d.Options, in.Options
		if old.Multiple != next.Multiple || old.LookupContainerID != next.LookupContainerID || old.TermSetID != next.TermSetID || old.ApplyParents != next.ApplyParents {
			return fmt.Errorf("%w: field cardinality, reference scope and parent application are immutable", dberr.ErrImmutable)
		}
		d.Label, d.Required, d.Indexed, d.Choices, d.Options, d.Version = in.Label, in.Required, in.Indexed, in.Choices, in.Options, version+1
		if d.Choices == nil {
			d.Choices = []string{}
		}
		out = d
		return saved(q.UpdateFieldDefinition(ctx, d))
	})
	return out, err
}

// DeleteField deletes a field no content type has.
func (r *Repo) DeleteField(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		if _, err := q.FieldDefinition(ctx, id); err != nil {
			return err
		}
		types, err := q.ContentTypes(ctx, ContentTypeFilter{FieldID: id})
		if err != nil {
			return err
		}
		if len(types) > 0 {
			return fmt.Errorf("%w: field definition %s is referenced by content type %s", dberr.ErrForeignKeyViolation, id, types[0].ID)
		}
		return q.DeleteFieldDefinition(ctx, id)
	})
}

// SetIndexStatus records the index status of fields; a single missing
// field is dberr.ErrNotFound.
func (r *Repo) SetIndexStatus(ctx context.Context, ids []string, status IndexStatus) error {
	if len(ids) == 0 {
		return nil
	}
	if !status.Valid() {
		return fmt.Errorf("%w: invalid index status %q", dberr.ErrCheckViolation, status)
	}
	return r.write(ctx, func(q Queries) error {
		n, err := q.SetFieldIndexStatus(ctx, ids, status)
		if err == nil && n == 0 && len(ids) == 1 {
			return dberr.ErrNotFound
		}
		return err
	})
}

func (r *Repo) ContentTypes(ctx context.Context, f ContentTypeFilter) ([]*ContentType, error) {
	return contentTypes(ctx, r.read, f)
}

// CatalogTypeIDs maps each existing type to the catalog type it is or
// copies. It reads the stored types only, without resolving their schemas.
func (r *Repo) CatalogTypeIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.read.ContentTypes(ctx, ContentTypeFilter{IDs: ids, Detached: true})
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		out[t.ID] = t.ID
		if !t.Catalog() {
			out[t.ID] = *t.ParentID
		}
	}
	return out, nil
}

// Derives reports whether a type is another type or derives from it. A list
// type named as the ancestor stands for the catalog type it copies.
func (r *Repo) Derives(ctx context.Context, typeID, ancestorID string) (bool, error) {
	catalogTypes, err := r.CatalogTypeIDs(ctx, []string{ancestorID})
	if err != nil {
		return false, err
	}
	want := catalogTypes[ancestorID]
	if want == "" || typeID == "" {
		return false, nil
	}
	if typeID == want {
		return true, nil
	}
	lineages, err := r.read.Lineages(ctx, []string{typeID})
	return slices.Contains(lineages[typeID], want), err
}

// ContentTypeKey returns the key of a stored type, or dberr.ErrNotFound.
func (r *Repo) ContentTypeKey(ctx context.Context, id string) (string, error) {
	t, err := r.read.ContentType(ctx, id)
	if err != nil {
		return "", err
	}
	return t.Key, nil
}

// ContentType returns a content type, or dberr.ErrNotFound.
func (r *Repo) ContentType(ctx context.Context, id string) (*ContentType, error) {
	return contentType(ctx, r.read, id)
}

// OneContentType returns the only content type f selects.
func (r *Repo) OneContentType(ctx context.Context, f ContentTypeFilter) (*ContentType, error) {
	return one(r.ContentTypes(ctx, f))
}

func (r *Repo) ContentTypeExists(ctx context.Context, f ContentTypeFilter) (bool, error) {
	rows, err := r.read.ContentTypes(ctx, f)
	return len(rows) > 0, err
}

func (r *Repo) CountContentTypes(ctx context.Context, f ContentTypeFilter) (int, error) {
	rows, err := r.read.ContentTypes(ctx, f)
	return len(rows), err
}

func contentTypes(ctx context.Context, q Queries, f ContentTypeFilter) ([]*ContentType, error) {
	rows, err := q.ContentTypes(ctx, f)
	if err != nil {
		return nil, err
	}
	return rows, resolve(ctx, q, rows)
}

func contentType(ctx context.Context, q Queries, id string) (*ContentType, error) {
	t, err := q.ContentType(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, resolve(ctx, q, []*ContentType{t})
}

// resolve fills in the lineage and effective schema of stored types. A list
// type shows the current name of its catalog type. Own fields an ancestor
// has, hidden fields no ancestor has, and the order and settings of fields
// the type no longer has drop out, as SaveContentType stores them once an
// ancestor changes.
func resolve(ctx context.Context, q Queries, types []*ContentType) error {
	if len(types) == 0 {
		return nil
	}
	ids := make([]string, len(types))
	for i, t := range types {
		ids[i] = t.ID
	}
	lineages, err := q.Lineages(ctx, ids)
	if err != nil {
		return err
	}
	wanted := []string{}
	for _, lineage := range lineages {
		wanted = append(wanted, lineage...)
	}
	slices.Sort(wanted)
	ancestors := map[string]*ContentType{}
	if wanted = slices.Compact(wanted); len(wanted) > 0 {
		rows, err := q.ContentTypes(ctx, ContentTypeFilter{IDs: wanted, Detached: true})
		if err != nil {
			return err
		}
		for _, row := range rows {
			ancestors[row.ID] = row
		}
	}
	for _, t := range types {
		t.AncestorIDs = append([]string{}, lineages[t.ID]...)
		chain := []*ContentType{}
		for i := len(t.AncestorIDs) - 1; i >= 0; i-- {
			a := ancestors[t.AncestorIDs[i]]
			if a == nil {
				return fmt.Errorf("%w: ancestor %s of content type %s", dberr.ErrNotFound, t.AncestorIDs[i], t.ID)
			}
			chain = append(chain, a)
		}
		inherited := effective(chain).FieldIds
		t.FieldIds = slices.DeleteFunc(slices.Clone(t.FieldIds), func(id string) bool { return slices.Contains(inherited, id) })
		t.HiddenFieldIds = slices.DeleteFunc(slices.Clone(t.HiddenFieldIds), func(id string) bool { return !slices.Contains(inherited, id) })
		t.Effective = effective(append(chain, t))
		all := t.Effective.FieldIds
		t.FieldOrder = slices.DeleteFunc(slices.Clone(t.FieldOrder), func(id string) bool { return !slices.Contains(all, id) })
		t.FieldSettings = slices.DeleteFunc(slices.Clone(t.FieldSettings), func(s FieldSetting) bool { return !slices.Contains(all, s.FieldID) })
		if !t.Catalog() && len(chain) > 0 {
			t.Name = chain[len(chain)-1].Name
		}
	}
	return nil
}

// effective combines a lineage, root first. A field appears once, where an
// ancestor first has it; hidden fields are inherited fields.
func effective(chain []*ContentType) Effective {
	out := Effective{FieldIds: []string{}, HiddenFieldIds: []string{}, Rules: []model.ValidationRule{}, FieldSettings: []FieldSetting{}}
	settings := map[string]FieldSetting{}
	for _, t := range chain {
		inherited := slices.Clone(out.FieldIds)
		for _, id := range t.HiddenFieldIds {
			if slices.Contains(inherited, id) && !slices.Contains(out.HiddenFieldIds, id) {
				out.HiddenFieldIds = append(out.HiddenFieldIds, id)
			}
		}
		for _, id := range t.FieldIds {
			if !slices.Contains(out.FieldIds, id) {
				out.FieldIds = append(out.FieldIds, id)
			}
		}
		out.FieldIds = reorder(out.FieldIds, t.FieldOrder)
		out.Rules = append(out.Rules, t.Rules...)
		for _, s := range t.FieldSettings {
			merged := settings[s.FieldID]
			merged.FieldID = s.FieldID
			if s.Required != nil {
				merged.Required = s.Required
			}
			if s.DefaultValue != nil {
				merged.DefaultValue = s.DefaultValue
			}
			settings[s.FieldID] = merged
		}
	}
	for _, id := range out.FieldIds {
		if s, ok := settings[id]; ok {
			out.FieldSettings = append(out.FieldSettings, s)
		}
	}
	return out
}

// reorder puts the ids order names first, in that order, and the others
// after them as they were.
func reorder(ids, order []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range order {
		if slices.Contains(ids, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// CollectionContentType returns a list type a collection accepts, named by
// its own id or by the catalog type it copies, or the default when id is
// empty.
func (r *Repo) CollectionContentType(ctx context.Context, collectionID, id string) (*ContentType, error) {
	types, err := r.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID})
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if id == "" && t.IsDefault || id != "" && (t.ID == id || *t.ParentID == id) {
			return t, nil
		}
	}
	return nil, dberr.ErrNotFound
}

// CreateContentType adds a catalog type. It derives from a catalog parent
// unless it is a root; its own fields exist and every field of its lineage
// has a distinct key.
func (r *Repo) CreateContentType(ctx context.Context, t ContentType) (*ContentType, error) {
	if t.CollectionID != nil {
		return nil, fmt.Errorf("%w: list types are made by attaching a catalog type", dberr.ErrInvalidReference)
	}
	var out *ContentType
	err := r.write(ctx, func(q Queries) error {
		var err error
		out, err = insertContentType(ctx, q, t)
		return err
	})
	return out, err
}

func insertContentType(ctx context.Context, q Queries, t ContentType) (*ContentType, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.FieldIds, t.HiddenFieldIds, t.Rules = nonNil(t.FieldIds), nonNil(t.HiddenFieldIds), nonNil(t.Rules)
	t.FieldOrder, t.FieldSettings = nonNil(t.FieldOrder), nonNil(t.FieldSettings)
	t.AncestorIDs = []string{}
	if t.ParentID != nil {
		parent, err := contentType(ctx, q, *t.ParentID)
		if dberr.IsNotFound(err) {
			return nil, fmt.Errorf("%w: parent content type %s does not exist", dberr.ErrInvalidReference, *t.ParentID)
		}
		if err != nil {
			return nil, err
		}
		if !parent.Catalog() {
			return nil, Violation("content types derive from catalog types")
		}
		t.AncestorIDs = append([]string{parent.ID}, parent.AncestorIDs...)
		if err = depth(t, len(t.AncestorIDs)); err != nil {
			return nil, err
		}
	}
	t.Version, t.Attached, t.InNewMenu, t.CreatedAt = 1, true, true, time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	if err := q.InsertContentType(ctx, &t); err != nil {
		return nil, err
	}
	out, err := contentType(ctx, q, t.ID)
	if err != nil {
		return nil, err
	}
	if err = kept(t, out); err != nil {
		return nil, err
	}
	return out, checkLineage(ctx, q, []*ContentType{out})
}

// depth checks that a type with that many ancestors fits within MaxDepth.
func depth(t ContentType, ancestors int) error {
	if t.Catalog() && ancestors > MaxDepth-1 {
		return Violation(fmt.Sprintf("catalog types have at most %d ancestors", MaxDepth-1))
	}
	if ancestors > MaxDepth {
		return Violation(fmt.Sprintf("content types have at most %d ancestors", MaxDepth))
	}
	return nil
}

// kept checks that resolving the written type kept the fields the write
// asked for: own fields are not inherited, hidden fields are, and ordered and
// set fields are the type's.
func kept(asked ContentType, t *ContentType) error {
	for _, id := range asked.FieldIds {
		if !slices.Contains(t.FieldIds, id) {
			return fmt.Errorf("%w: content type %s inherits field %s", dberr.ErrInvalidReference, t.Key, id)
		}
	}
	for _, id := range asked.HiddenFieldIds {
		if !slices.Contains(t.HiddenFieldIds, id) {
			return fmt.Errorf("%w: hidden field %s is not inherited by content type %s", dberr.ErrInvalidReference, id, t.Key)
		}
	}
	for _, id := range asked.FieldOrder {
		if !slices.Contains(t.FieldOrder, id) {
			return fmt.Errorf("%w: content type %s orders field %s it does not have", dberr.ErrInvalidReference, t.Key, id)
		}
	}
	for _, s := range asked.FieldSettings {
		if !slices.ContainsFunc(t.FieldSettings, func(kept FieldSetting) bool { return kept.FieldID == s.FieldID }) {
			return fmt.Errorf("%w: content type %s sets field %s it does not have", dberr.ErrInvalidReference, t.Key, s.FieldID)
		}
	}
	return nil
}

// RecordLineage records the lineage of a type stored without one, such as
// an imported row, whose parent has its lineage.
func (r *Repo) RecordLineage(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		t, err := q.ContentType(ctx, id)
		if err != nil {
			return err
		}
		ancestors := []string{}
		if t.ParentID != nil {
			lineages, err := q.Lineages(ctx, []string{*t.ParentID})
			if err != nil {
				return err
			}
			ancestors = append([]string{*t.ParentID}, lineages[*t.ParentID]...)
		}
		if err = depth(*t, len(ancestors)); err != nil {
			return err
		}
		return q.InsertLineage(ctx, id, ancestors)
	})
}

// sameFields reports whether two versions of a type store the same fields,
// hidden fields, field order and field settings.
func sameFields(a, b *ContentType) bool {
	return slices.Equal(a.FieldIds, b.FieldIds) && slices.Equal(a.HiddenFieldIds, b.HiddenFieldIds) && slices.Equal(a.FieldOrder, b.FieldOrder) &&
		slices.EqualFunc(a.FieldSettings, b.FieldSettings, func(x, y FieldSetting) bool {
			return x.FieldID == y.FieldID && (x.Required == nil) == (y.Required == nil) && (x.Required == nil || *x.Required == *y.Required) && string(x.DefaultValue) == string(y.DefaultValue)
		})
}

// SaveContentType changes a type's own fields, hidden fields, rules, field
// order and field settings at version, and a catalog type's name and group. Its key and parent never change. The
// type and every type deriving from it keep distinct field keys. When its own
// fields change, deriving types store theirs without the fields it now has
// and their hidden fields without those it no longer has.
func (r *Repo) SaveContentType(ctx context.Context, in ContentType, version int) (*ContentType, error) {
	var out *ContentType
	err := r.write(ctx, func(q Queries) error {
		t, err := q.ContentType(ctx, in.ID)
		if err != nil {
			return err
		}
		if t.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if t.Catalog() {
			t.Name, t.Group = in.Name, in.Group
		}
		reshaped := !slices.Equal(t.FieldIds, nonNil(in.FieldIds))
		t.FieldIds, t.HiddenFieldIds, t.Rules = nonNil(in.FieldIds), nonNil(in.HiddenFieldIds), nonNil(in.Rules)
		t.FieldOrder, t.FieldSettings = nonNil(in.FieldOrder), nonNil(in.FieldSettings)
		t.Version, t.UpdatedAt = version+1, time.Now().UTC()
		if err = saved(q.UpdateContentType(ctx, t)); err != nil {
			return err
		}
		if out, err = contentType(ctx, q, t.ID); err != nil {
			return err
		}
		if err = kept(*t, out); err != nil {
			return err
		}
		ids, err := q.Descendants(ctx, []string{t.ID})
		if err != nil {
			return err
		}
		others := slices.DeleteFunc(ids, func(id string) bool { return id == t.ID })
		checked, stored := []*ContentType{out}, []string{}
		if reshaped && len(others) > 0 {
			rows, err := q.ContentTypes(ctx, ContentTypeFilter{IDs: others, Detached: true})
			if err != nil {
				return err
			}
			before := make([]ContentType, len(rows))
			for i, row := range rows {
				before[i] = *row
			}
			if err = resolve(ctx, q, rows); err != nil {
				return err
			}
			for i, row := range rows {
				if sameFields(&before[i], row) {
					continue
				}
				row.Version, row.UpdatedAt = row.Version+1, t.UpdatedAt
				if err = saved(q.UpdateContentType(ctx, row)); err != nil {
					return err
				}
				stored = append(stored, row.ID)
			}
			checked = append(checked, rows...)
		}
		if err = checkLineage(ctx, q, checked); err != nil {
			return err
		}
		others = slices.DeleteFunc(others, func(id string) bool { return slices.Contains(stored, id) })
		if len(others) == 0 {
			return nil
		}
		return q.BumpContentTypes(ctx, others, t.UpdatedAt)
	})
	return out, err
}

// checkLineage checks that the types' own fields exist and are distinct, and
// that each type's effective fields have distinct keys and fit MaxFields.
func checkLineage(ctx context.Context, q Queries, types []*ContentType) error {
	ids := []string{}
	for _, t := range types {
		ids = append(append(ids, t.FieldIds...), t.Effective.FieldIds...)
	}
	slices.Sort(ids)
	rows, err := q.FieldDefinitions(ctx, FieldFilter{IDs: slices.Compact(ids)})
	if err != nil {
		return err
	}
	fields := make(map[string]*FieldDefinition, len(rows))
	for _, row := range rows {
		fields[row.ID] = row
	}
	for _, t := range types {
		if err := distinctFields(fields, t.FieldIds); err != nil {
			return err
		}
		if len(t.Effective.FieldIds) > MaxFields {
			return Violation(fmt.Sprintf("content type %q would have more than %d fields with its ancestors", t.Key, MaxFields))
		}
		keys := map[string]string{}
		for _, id := range t.Effective.FieldIds {
			field := fields[id]
			if field == nil {
				return fmt.Errorf("%w: field %s does not exist", dberr.ErrInvalidReference, id)
			}
			if other, taken := keys[field.Key]; taken && other != id {
				return Violation(fmt.Sprintf("field key %q appears twice in content type %q and its ancestors", field.Key, t.Key))
			}
			keys[field.Key] = id
		}
	}
	return nil
}

func distinctFields(fields map[string]*FieldDefinition, ids []string) error {
	seenIDs, seenKeys := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		field := fields[id]
		if field == nil {
			return fmt.Errorf("%w: field %s does not exist", dberr.ErrInvalidReference, id)
		}
		if seenIDs[id] || seenKeys[field.Key] {
			return fmt.Errorf("%w: content type fields require distinct identities and keys", dberr.ErrInvalidReference)
		}
		seenIDs[id], seenKeys[field.Key] = true, true
	}
	return nil
}

// BumpContentTypesForField advances the versions of the content types that
// have a field, so their ETags change with it.
func (r *Repo) BumpContentTypesForField(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		types, err := q.ContentTypes(ctx, ContentTypeFilter{FieldID: id, Detached: true})
		if err != nil || len(types) == 0 {
			return err
		}
		ids := make([]string, len(types))
		for i, t := range types {
			ids[i] = t.ID
		}
		if ids, err = q.Descendants(ctx, ids); err != nil {
			return err
		}
		return q.BumpContentTypes(ctx, ids, time.Now().UTC())
	})
}

// Descendants lists the types and every type deriving from them.
func (r *Repo) Descendants(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	return r.read.Descendants(ctx, ids)
}

// Accepts reports whether a collection accepts a content type.
func (r *Repo) Accepts(ctx context.Context, collectionID, contentTypeID string) (bool, error) {
	return Accepts(ctx, r.read, collectionID, contentTypeID)
}

// Accepts reports whether a collection accepts a content type, on q: the
// type is one of the collection's attached list types.
func Accepts(ctx context.Context, q Queries, collectionID, contentTypeID string) (bool, error) {
	t, err := q.ContentType(ctx, contentTypeID)
	if dberr.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return t.CollectionID != nil && *t.CollectionID == collectionID && t.Attached, nil
}

// Counterpart returns the type a collection accepts in place of a list type
// of another collection: the type itself when the collection accepts it,
// otherwise the collection's list type of the same catalog type, or
// dberr.ErrNotFound.
func (r *Repo) Counterpart(ctx context.Context, collectionID, contentTypeID string) (string, error) {
	return Counterpart(ctx, r.read, collectionID, contentTypeID)
}

// Counterpart is Repo.Counterpart on q.
func Counterpart(ctx context.Context, q Queries, collectionID, contentTypeID string) (string, error) {
	t, err := q.ContentType(ctx, contentTypeID)
	if err != nil {
		return "", err
	}
	if t.CollectionID != nil && *t.CollectionID == collectionID && t.Attached {
		return t.ID, nil
	}
	if t.Catalog() || t.ParentID == nil {
		return "", dberr.ErrNotFound
	}
	types, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID, ParentID: *t.ParentID})
	if err != nil {
		return "", err
	}
	if len(types) == 0 {
		return "", dberr.ErrNotFound
	}
	return types[0].ID, nil
}

// AttachedCollections lists the collections that accept a type deriving
// from any of the types.
func (r *Repo) AttachedCollections(ctx context.Context, contentTypeIDs []string) ([]string, error) {
	if len(contentTypeIDs) == 0 {
		return []string{}, nil
	}
	return r.read.AttachedCollections(ctx, contentTypeIDs)
}

// Attach makes a live list or library accept a catalog type through its own
// list type, as its default when isDefault is set. Attaching a type the
// collection accepted before restores that list type with its
// customizations. A collection accepts at most MaxCollectionTypes types.
// changed is false when the collection already accepted the type that way.
func (r *Repo) Attach(ctx context.Context, collectionID, contentTypeID string, isDefault bool) (out *ContentType, changed bool, err error) {
	err = r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, collectionID); err != nil {
			return err
		}
		if err := collection(ctx, q, collectionID); err != nil {
			return err
		}
		parent, err := q.ContentType(ctx, contentTypeID)
		if err != nil {
			return err
		}
		if !parent.Catalog() {
			return Violation("collections attach catalog types")
		}
		attached, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID})
		if err != nil {
			return err
		}
		known, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID, ParentID: parent.ID, Detached: true})
		if err != nil {
			return err
		}
		var current *ContentType
		if len(known) > 0 {
			current = known[0]
		}
		if current != nil && current.Attached && (!isDefault || current.IsDefault) {
			out, err = contentType(ctx, q, current.ID)
			return err
		}
		changed = true
		if current == nil || !current.Attached {
			if len(attached) >= MaxCollectionTypes {
				return Violation(fmt.Sprintf("at most %d content types per collection", MaxCollectionTypes))
			}
			// A newly accepted type comes last in the New menu.
			position := 0
			for _, t := range attached {
				position = max(position, t.MenuPosition+1)
			}
			if current == nil {
				id := collectionID
				if current, err = insertContentType(ctx, q, ContentType{Key: parent.Key, Name: parent.Name, ParentID: &parent.ID, CollectionID: &id, MenuPosition: position}); err != nil {
					return err
				}
			} else if err = q.SetAttached(ctx, current.ID, true); err != nil {
				return err
			} else if err = q.SetNewMenu(ctx, current.ID, true, position); err != nil {
				return err
			}
		}
		if isDefault || len(attached) == 0 {
			if err = q.SetDefaultContentType(ctx, collectionID, current.ID); err != nil {
				return err
			}
		}
		out, err = contentType(ctx, q, current.ID)
		return err
	})
	return out, changed, err
}

// Detach makes a collection stop accepting a list type that none of its live
// resources has. The collection keeps at least one type; when its default
// goes, the remaining type with the lowest key becomes the default. The list
// type stays for the history of its items.
func (r *Repo) Detach(ctx context.Context, collectionID, contentTypeID string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, collectionID); err != nil {
			return err
		}
		attached, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID})
		if err != nil {
			return err
		}
		var gone *ContentType
		remaining := []*ContentType{}
		for _, t := range attached {
			if t.ID == contentTypeID {
				gone = t
			} else {
				remaining = append(remaining, t)
			}
		}
		if gone == nil {
			return dberr.ErrNotFound
		}
		if len(remaining) == 0 {
			return ErrLastContentType
		}
		inUse, err := q.ContentTypeInUse(ctx, collectionID, contentTypeID)
		if err != nil {
			return err
		}
		if inUse {
			return ErrContentTypeInUse
		}
		if err := q.SetAttached(ctx, gone.ID, false); err != nil || !gone.IsDefault {
			return err
		}
		return q.SetDefaultContentType(ctx, collectionID, remaining[0].ID)
	})
}

// SetNewMenu offers the collection's list types that ids name, by their own
// id or the catalog type they copy, in that order when people create items,
// and no others. The menu keeps the default and at least one type. It
// returns the collection's types in menu order.
func (r *Repo) SetNewMenu(ctx context.Context, collectionID string, ids []string) (out []*ContentType, err error) {
	err = r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, collectionID); err != nil {
			return err
		}
		if err := collection(ctx, q, collectionID); err != nil {
			return err
		}
		attached, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID})
		if err != nil {
			return err
		}
		positions := map[string]int{}
		for i, id := range ids {
			j := slices.IndexFunc(attached, func(t *ContentType) bool { return t.ID == id || *t.ParentID == id })
			if j < 0 {
				return dberr.ErrNotFound
			}
			if _, seen := positions[attached[j].ID]; seen {
				return Violation("the New menu lists each content type once")
			}
			positions[attached[j].ID] = i
		}
		if len(positions) == 0 {
			return Violation("the New menu offers at least one content type")
		}
		for _, t := range attached {
			position, offered := positions[t.ID]
			if t.IsDefault && !offered {
				return Violation("the New menu offers the default content type")
			}
			if err = q.SetNewMenu(ctx, t.ID, offered, position); err != nil {
				return err
			}
		}
		if out, err = contentTypes(ctx, q, ContentTypeFilter{CollectionID: collectionID}); err == nil {
			MenuOrder(out)
		}
		return err
	})
	return out, err
}

// MenuOrder sorts a collection's list types as its New menu offers them,
// followed by those it leaves out, by key.
func MenuOrder(types []*ContentType) {
	slices.SortStableFunc(types, func(a, b *ContentType) int {
		switch {
		case a.InNewMenu != b.InNewMenu:
			if a.InNewMenu {
				return -1
			}
			return 1
		case a.InNewMenu && a.MenuPosition != b.MenuPosition:
			return a.MenuPosition - b.MenuPosition
		}
		return strings.Compare(a.Key, b.Key)
	})
}

// DeleteContentType deletes a catalog type at version that no type derives
// from: it has no subtypes and no list type, attached or kept for history.
// The root never goes.
func (r *Repo) DeleteContentType(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		t, err := q.ContentType(ctx, id)
		if err != nil {
			return err
		}
		if t.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if !t.Catalog() {
			return Violation("list types go when their collection detaches the catalog type")
		}
		if t.ParentID == nil {
			return Violation("the root content type cannot be deleted")
		}
		ids, err := q.Descendants(ctx, []string{id})
		if err != nil {
			return err
		}
		derived, err := q.ContentTypes(ctx, ContentTypeFilter{IDs: slices.DeleteFunc(ids, func(other string) bool { return other == id }), Detached: true})
		if err != nil {
			return err
		}
		for _, d := range derived {
			if d.Catalog() {
				return Violation(fmt.Sprintf("delete the subtypes of content type %q first", t.Key))
			}
		}
		if len(derived) > 0 {
			return Violation(fmt.Sprintf("lists use content type %q or keep it for their items' history", t.Key))
		}
		return q.DeleteContentType(ctx, id)
	})
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// collection checks that id is a live list or library.
func collection(ctx context.Context, q Queries, id string) error {
	c, err := q.Resource(ctx, id)
	if dberr.IsNotFound(err) {
		return fmt.Errorf("%w: collection not found", dberr.ErrInvalidContainment)
	}
	if err != nil {
		return err
	}
	if c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
		return fmt.Errorf("%w: content types attach to lists and libraries", dberr.ErrInvalidContainment)
	}
	if c.Deleted {
		return fmt.Errorf("%w: deleted collections cannot be configured", dberr.ErrImmutable)
	}
	return nil
}

func saved(ok bool, err error) error {
	if err == nil && !ok {
		return dberr.ErrConcurrentUpdate
	}
	return err
}

func one[T any](rows []T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, dberr.ErrNotFound
	}
	if len(rows) != 1 {
		return zero, dberr.ErrUniqueViolation
	}
	return rows[0], nil
}
