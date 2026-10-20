// Package catalog owns the organization's field definitions and content
// types, and the content types each collection accepts. Drivers implement
// Queries; this package decides what those statements mean.
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

// MaxCollectionTypes bounds the content types a collection accepts.
const MaxCollectionTypes = 32

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

// ContentType is an organization content type: the fields its items carry
// and the rules their values follow.
type ContentType struct {
	ID          string                 `json:"id,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
	Key         string                 `json:"key,omitempty"`
	Name        string                 `json:"name,omitempty"`
	TemplateKey *string                `json:"template_key,omitempty"`
	FieldIds    []string               `json:"field_ids,omitempty"`
	Rules       []model.ValidationRule `json:"rules,omitempty"`
	Version     int                    `json:"version,omitempty"`
}

// Attachment is a content type a collection accepts; one is its default.
type Attachment struct {
	ID, CollectionID, ContentTypeID string
	IsDefault                       bool
	CreatedAt                       time.Time
}

// FieldFilter selects fields ordered by key. A nil list does not filter;
// an empty one selects nothing.
type FieldFilter struct {
	IDs, Keys    []string
	Types        []FieldType
	CollectionID string
	ExcludeType  FieldType
}

// ContentTypeFilter selects content types ordered by key. A nil IDs does
// not filter; an empty one selects nothing.
type ContentTypeFilter struct {
	IDs                                []string
	CollectionID, TemplateKey, FieldID string
	// CollectionIDs selects the types any of the collections accept; an
	// empty list selects nothing.
	CollectionIDs []string
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
	ContentTypes(ctx context.Context, f ContentTypeFilter) ([]*ContentType, error)
	ContentType(ctx context.Context, id string) (*ContentType, error)
	InsertContentType(ctx context.Context, t *ContentType) error
	// UpdateContentType writes t's name, fields and rules when its version
	// still is t.Version-1.
	UpdateContentType(ctx context.Context, t *ContentType) (bool, error)
	// BumpContentTypes advances the versions of content types.
	BumpContentTypes(ctx context.Context, ids []string, at time.Time) error
	// Attachments lists a collection's attachments by content type id.
	Attachments(ctx context.Context, collectionID string) ([]Attachment, error)
	InsertAttachment(ctx context.Context, a Attachment) error
	SetDefaultAttachment(ctx context.Context, collectionID, contentTypeID string) error
	DeleteAttachment(ctx context.Context, id string) error
	// AttachedCollections lists the collections that accept any of the types.
	AttachedCollections(ctx context.Context, contentTypeIDs []string) ([]string, error)
	// ContentTypeInUse reports whether live resources of the collection have
	// the content type.
	ContentTypeInUse(ctx context.Context, collectionID, contentTypeID string) (bool, error)
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

func (r *Repo) Fields(ctx context.Context, f FieldFilter) ([]*FieldDefinition, error) {
	return r.read.FieldDefinitions(ctx, f)
}

// Field returns a field, or dberr.ErrNotFound.
func (r *Repo) Field(ctx context.Context, id string) (*FieldDefinition, error) {
	return r.read.FieldDefinition(ctx, id)
}

// OneField returns the only field f selects: dberr.ErrNotFound when none
// does, dberr.ErrUniqueViolation when several do.
func (r *Repo) OneField(ctx context.Context, f FieldFilter) (*FieldDefinition, error) {
	return one(r.read.FieldDefinitions(ctx, f))
}

func (r *Repo) FieldExists(ctx context.Context, f FieldFilter) (bool, error) {
	rows, err := r.read.FieldDefinitions(ctx, f)
	return len(rows) > 0, err
}

func (r *Repo) CountFields(ctx context.Context, f FieldFilter) (int, error) {
	rows, err := r.read.FieldDefinitions(ctx, f)
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
	return nil
}

// SaveField changes a field's label, flags, choices and options at
// version. Cardinality and reference scope never change.
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
		if old.Multiple != next.Multiple || old.LookupContainerID != next.LookupContainerID || old.TermSetID != next.TermSetID {
			return fmt.Errorf("%w: field cardinality and reference scope are immutable", dberr.ErrImmutable)
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

// DeleteField deletes a field no content type uses.
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
	return r.read.ContentTypes(ctx, f)
}

// ContentType returns a content type, or dberr.ErrNotFound.
func (r *Repo) ContentType(ctx context.Context, id string) (*ContentType, error) {
	return r.read.ContentType(ctx, id)
}

// OneContentType returns the only content type f selects.
func (r *Repo) OneContentType(ctx context.Context, f ContentTypeFilter) (*ContentType, error) {
	return one(r.read.ContentTypes(ctx, f))
}

func (r *Repo) ContentTypeExists(ctx context.Context, f ContentTypeFilter) (bool, error) {
	rows, err := r.read.ContentTypes(ctx, f)
	return len(rows) > 0, err
}

func (r *Repo) CountContentTypes(ctx context.Context, f ContentTypeFilter) (int, error) {
	rows, err := r.read.ContentTypes(ctx, f)
	return len(rows), err
}

// CollectionContentType returns a content type a collection accepts, or its
// default when id is empty.
func (r *Repo) CollectionContentType(ctx context.Context, collectionID, id string) (*ContentType, error) {
	attachments, err := r.read.Attachments(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	for _, a := range attachments {
		if id == "" && a.IsDefault || id != "" && a.ContentTypeID == id {
			return r.read.ContentType(ctx, a.ContentTypeID)
		}
	}
	return nil, dberr.ErrNotFound
}

// CreateContentType adds a content type over existing fields with distinct
// keys.
func (r *Repo) CreateContentType(ctx context.Context, t ContentType) (*ContentType, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.FieldIds == nil {
		t.FieldIds = []string{}
	}
	if t.Rules == nil {
		t.Rules = []model.ValidationRule{}
	}
	t.Version, t.CreatedAt = 1, time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	return &t, r.write(ctx, func(q Queries) error {
		if err := distinctFields(ctx, q, t.FieldIds); err != nil {
			return err
		}
		return q.InsertContentType(ctx, &t)
	})
}

// SaveContentType changes a content type's name, fields and rules at
// version. Its key never changes.
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
		if err := distinctFields(ctx, q, in.FieldIds); err != nil {
			return err
		}
		t.Name, t.FieldIds, t.Rules, t.Version, t.UpdatedAt = in.Name, in.FieldIds, in.Rules, version+1, time.Now().UTC()
		if t.FieldIds == nil {
			t.FieldIds = []string{}
		}
		if t.Rules == nil {
			t.Rules = []model.ValidationRule{}
		}
		out = t
		return saved(q.UpdateContentType(ctx, t))
	})
	return out, err
}

func distinctFields(ctx context.Context, q Queries, ids []string) error {
	seenIDs, seenKeys := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		field, err := q.FieldDefinition(ctx, id)
		if dberr.IsNotFound(err) {
			return fmt.Errorf("%w: field %s does not exist", dberr.ErrInvalidReference, id)
		}
		if err != nil {
			return err
		}
		if seenIDs[id] || seenKeys[field.Key] {
			return fmt.Errorf("%w: content type fields require distinct identities and keys", dberr.ErrInvalidReference)
		}
		seenIDs[id], seenKeys[field.Key] = true, true
	}
	return nil
}

// BumpContentTypesForField advances the versions of the content types that
// use a field, so their ETags change with it.
func (r *Repo) BumpContentTypesForField(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		types, err := q.ContentTypes(ctx, ContentTypeFilter{FieldID: id})
		if err != nil || len(types) == 0 {
			return err
		}
		ids := make([]string, len(types))
		for i, t := range types {
			ids[i] = t.ID
		}
		return q.BumpContentTypes(ctx, ids, time.Now().UTC())
	})
}

// Attachments lists the content types a collection accepts.
func (r *Repo) Attachments(ctx context.Context, collectionID string) ([]Attachment, error) {
	return r.read.Attachments(ctx, collectionID)
}

// Accepts reports whether a collection accepts a content type.
func (r *Repo) Accepts(ctx context.Context, collectionID, contentTypeID string) (bool, error) {
	return Accepts(ctx, r.read, collectionID, contentTypeID)
}

// Accepts reports whether a collection accepts a content type, on q.
func Accepts(ctx context.Context, q Queries, collectionID, contentTypeID string) (bool, error) {
	attachments, err := q.Attachments(ctx, collectionID)
	for _, a := range attachments {
		if a.ContentTypeID == contentTypeID {
			return true, err
		}
	}
	return false, err
}

// AttachedCollections lists the collections that accept any of the types.
func (r *Repo) AttachedCollections(ctx context.Context, contentTypeIDs []string) ([]string, error) {
	if len(contentTypeIDs) == 0 {
		return []string{}, nil
	}
	return r.read.AttachedCollections(ctx, contentTypeIDs)
}

// Attach makes a live list or library accept a content type, as its default
// when isDefault is set. Keys stay unique within a collection, and a
// collection accepts at most MaxCollectionTypes types. changed is false when
// the collection already accepted the type that way.
func (r *Repo) Attach(ctx context.Context, collectionID, contentTypeID string, isDefault bool) (changed bool, err error) {
	err = r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, collectionID); err != nil {
			return err
		}
		if err := collection(ctx, q, collectionID); err != nil {
			return err
		}
		typ, err := q.ContentType(ctx, contentTypeID)
		if err != nil {
			return err
		}
		attachments, err := q.Attachments(ctx, collectionID)
		if err != nil {
			return err
		}
		var current *Attachment
		for i, a := range attachments {
			if a.ContentTypeID == contentTypeID {
				current = &attachments[i]
			}
		}
		if current != nil && (!isDefault || current.IsDefault) {
			return nil
		}
		changed = true
		if current == nil {
			if len(attachments) >= MaxCollectionTypes {
				return Violation(fmt.Sprintf("at most %d content types per collection", MaxCollectionTypes))
			}
			others, err := q.ContentTypes(ctx, ContentTypeFilter{CollectionID: collectionID})
			if err != nil {
				return err
			}
			for _, other := range others {
				if other.Key == typ.Key {
					return fmt.Errorf("%w: duplicate collection content type key %q", dberr.ErrUniqueViolation, typ.Key)
				}
			}
			err = q.InsertAttachment(ctx, Attachment{ID: uuid.NewString(), CollectionID: collectionID, ContentTypeID: contentTypeID, CreatedAt: time.Now().UTC()})
			if err != nil || !isDefault && len(attachments) > 0 {
				return err
			}
		}
		return q.SetDefaultAttachment(ctx, collectionID, contentTypeID)
	})
	return changed, err
}

// Detach makes a collection stop accepting a content type that none of its
// live resources has. The collection keeps at least one type; when its
// default goes, the remaining type with the lowest id becomes the default.
func (r *Repo) Detach(ctx context.Context, collectionID, contentTypeID string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, collectionID); err != nil {
			return err
		}
		attachments, err := q.Attachments(ctx, collectionID)
		if err != nil {
			return err
		}
		var gone *Attachment
		remaining := []Attachment{}
		for i, a := range attachments {
			if a.ContentTypeID == contentTypeID {
				gone = &attachments[i]
			} else {
				remaining = append(remaining, a)
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
		if err := q.DeleteAttachment(ctx, gone.ID); err != nil || !gone.IsDefault {
			return err
		}
		return q.SetDefaultAttachment(ctx, collectionID, remaining[0].ContentTypeID)
	})
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
