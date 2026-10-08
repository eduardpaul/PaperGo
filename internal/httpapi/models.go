package httpapi

import (
	"encoding/json"
	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/model"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// The response models below are the wire contract. Handlers convert Ent
// entities into them, so persistence fields and edges never reach clients and
// every documented field is always present unless marked optional.

// Page is a cursor-paginated list.
type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty" doc:"Opaque cursor for the next page; absent on the last page."`
}

// Unpaged is a complete list, or one page bounded by a revision or version cursor.
type Unpaged[T any] struct {
	Data []T `json:"data"`
}

// JSONObject is an immutable JSON object stored and returned verbatim.
type JSONObject json.RawMessage

func (o JSONObject) MarshalJSON() ([]byte, error) {
	if len(o) == 0 {
		return []byte("{}"), nil
	}
	return o, nil
}
func (JSONObject) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true}
}

type Health struct {
	Status string `json:"status" enum:"ok"`
}

type Resource struct {
	ID                  string         `json:"id" format:"uuid"`
	WorkspaceID         string         `json:"workspace_id" format:"uuid"`
	ParentID            *string        `json:"parent_id,omitempty" format:"uuid" doc:"Absent for workspaces."`
	ContainerID         *string        `json:"container_id,omitempty" format:"uuid" doc:"List or library that owns a folder or item."`
	ContentTypeID       *string        `json:"content_type_id,omitempty" format:"uuid" doc:"Immutable item content type assignment. Absent for other resource kinds."`
	Kind                string         `json:"kind" enum:"workspace,list,library,folder,item"`
	Name                string         `json:"name" doc:"Library folders and items are file names: unique among live siblings ignoring case, without / \\ or control characters."`
	Tags                []string       `json:"tags"`
	Values              map[string]any `json:"values" doc:"Values validated by the revision schema. Integers remain exact JSON numbers; decimals are canonical strings. Use an exact-number parser for 64-bit JSON integers."`
	InheritPermissions  bool           `json:"inherit_permissions"`
	Version             int            `json:"version" minimum:"1" doc:"Optimistic resource lock version, independent of content revision_number and blob version. Also returned as the ETag."`
	HeadRevisionID      *string        `json:"head_revision_id,omitempty" format:"uuid" doc:"Omitted from published read surfaces."`
	PublishedRevisionID *string        `json:"published_revision_id,omitempty" format:"uuid"`
	SchemaHeadID        *string        `json:"schema_head_id,omitempty" format:"uuid" doc:"Current schema revision of a list or library."`
	NextRevisionNumber  int            `json:"next_revision_number,omitempty" minimum:"1" doc:"Internal content counter of items; omitted from published read surfaces."`
	PublishingEnabled   bool           `json:"publishing_enabled" doc:"Lists/libraries only. False publishes content changes immediately."`
	WebDAVEnabled       bool           `json:"webdav_enabled" doc:"Libraries only. Serves the library's folders and files at /webdav/{id}/."`
	CreatedAt           time.Time      `json:"created_at"`
	CreatedBy           string         `json:"created_by"`
	UpdatedAt           time.Time      `json:"updated_at" doc:"For items, creation timestamp of the selected content revision."`
	UpdatedBy           string         `json:"updated_by" doc:"For items, the actor of the selected content revision."`
}

func resourceOut(r *ent.Resource) Resource {
	values := r.Values
	if values == nil {
		values = map[string]any{}
	}
	return Resource{ID: r.ID, WorkspaceID: r.WorkspaceID, ParentID: r.ParentID, ContainerID: r.ContainerID, ContentTypeID: r.ContentTypeID, Kind: string(r.Kind), Name: r.Name, Tags: nonNil(r.Tags), Values: values, InheritPermissions: r.InheritPermissions, Version: r.Version, HeadRevisionID: r.HeadRevisionID, PublishedRevisionID: r.PublishedRevisionID, SchemaHeadID: r.SchemaHeadID, NextRevisionNumber: r.NextRevisionNumber, PublishingEnabled: r.PublishingEnabled, WebDAVEnabled: r.WebdavEnabled, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}
}

type FieldDefinition struct {
	ID          string             `json:"id" format:"uuid"`
	ContainerID string             `json:"container_id" format:"uuid"`
	Key         string             `json:"key" doc:"Immutable field key."`
	Label       string             `json:"label"`
	Type        string             `json:"type" enum:"text,note,email,url,date,datetime,choice,integer,decimal,number,boolean,lookup,term"`
	Required    bool               `json:"required"`
	Choices     []string           `json:"choices"`
	Indexed     bool               `json:"indexed" doc:"Opt-in typed query projection."`
	Scale       int                `json:"scale" minimum:"0" maximum:"9" doc:"Immutable decimal scale; zero for all other field types."`
	Options     model.FieldOptions `json:"options"`
	CreatedAt   time.Time          `json:"created_at"`
}

func fieldOut(d *ent.FieldDefinition) FieldDefinition {
	return FieldDefinition{ID: d.ID, ContainerID: d.ContainerID, Key: d.Key, Label: d.Label, Type: string(d.Type), Required: d.Required, Choices: nonNil(d.Choices), Indexed: d.Indexed, Scale: d.Scale, Options: d.Options, CreatedAt: d.CreatedAt}
}

type Relationship struct {
	ID          string         `json:"id" format:"uuid"`
	WorkspaceID string         `json:"workspace_id" format:"uuid"`
	SourceID    string         `json:"source_id" format:"uuid"`
	TargetID    string         `json:"target_id" format:"uuid"`
	TypeID      string         `json:"type_id" format:"uuid"`
	Name        string         `json:"name" doc:"Key of the relationship type."`
	Directed    bool           `json:"directed"`
	Metadata    map[string]any `json:"metadata" doc:"Attribute values validated by the relationship type."`
	Version     int            `json:"version" minimum:"1" doc:"Edge lock version, also returned as the ETag."`
	CreatedAt   time.Time      `json:"created_at"`
}

func relationshipOut(r *ent.Relationship) Relationship {
	metadata := r.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	return Relationship{ID: r.ID, WorkspaceID: r.WorkspaceID, SourceID: r.SourceID, TargetID: r.TargetID, TypeID: r.TypeID, Name: r.Name, Directed: r.Directed, Metadata: metadata, Version: r.Version, CreatedAt: r.CreatedAt}
}

type Publication struct {
	ID          string     `json:"id" format:"uuid"`
	ItemID      string     `json:"item_id" format:"uuid"`
	Action      string     `json:"action" enum:"publish,unpublish"`
	RevisionID  *string    `json:"revision_id,omitempty" format:"uuid"`
	Version     int        `json:"version" minimum:"1" doc:"Resource lock version consumed by this event. Explicit lifecycle returns ETag version+1."`
	PublishedBy string     `json:"published_by"`
	Snapshot    JSONObject `json:"snapshot" doc:"Published item name, tags, values and revision references."`
	CreatedAt   time.Time  `json:"created_at"`
}

func publicationOut(p *ent.Publication) Publication {
	return Publication{ID: p.ID, ItemID: p.ItemID, Action: string(p.Action), RevisionID: p.RevisionID, Version: p.Version, PublishedBy: p.PublishedBy, Snapshot: JSONObject(p.Snapshot), CreatedAt: p.CreatedAt}
}

type Blob struct {
	ID              string `json:"id" format:"uuid"`
	ItemID          string `json:"item_id" format:"uuid"`
	Version         int    `json:"version" minimum:"1" doc:"Immutable blob sequence number, independent of the resource lock version and content revision number."`
	ResourceVersion int    `json:"resource_version" minimum:"1" doc:"Resource lock version after upload; also returned as ETag."`
	Filename        string `json:"filename"`
	ContentType     string `json:"content_type"`
	Size            int64  `json:"size" minimum:"0"`
	SHA256          string `json:"sha256" doc:"Hex SHA-256 of the bytes; downloads return it as the ETag."`
}

type AuditEvent struct {
	ID          string         `json:"id" format:"uuid"`
	WorkspaceID string         `json:"workspace_id" format:"uuid"`
	ResourceID  string         `json:"resource_id"`
	Subject     string         `json:"subject"`
	Action      string         `json:"action"`
	Details     map[string]any `json:"details"`
	CreatedAt   time.Time      `json:"created_at"`
}

func auditOut(e *ent.AuditEvent) AuditEvent {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	return AuditEvent{ID: e.ID, WorkspaceID: e.WorkspaceID, ResourceID: e.ResourceID, Subject: e.Subject, Action: e.Action, Details: details, CreatedAt: e.CreatedAt}
}

type ItemRevision struct {
	ID               string          `json:"id" format:"uuid"`
	ItemID           string          `json:"item_id" format:"uuid"`
	ContainerID      string          `json:"container_id" format:"uuid"`
	ContentTypeID    string          `json:"content_type_id" format:"uuid"`
	SchemaRevisionID string          `json:"schema_revision_id" format:"uuid"`
	BlobID           *string         `json:"blob_id,omitempty" format:"uuid"`
	RevisionNumber   int             `json:"revision_number" minimum:"1"`
	Name             string          `json:"name"`
	Tags             []string        `json:"tags"`
	Payload          JSONObject      `json:"payload" doc:"Exact field values of this revision."`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
	SchemaRevision   *SchemaRevision `json:"schema_revision,omitempty" doc:"Schema snapshot for interpreting historical values."`
}

func revisionOut(r *ent.ItemRevision) (ItemRevision, error) {
	out := ItemRevision{ID: r.ID, ItemID: r.ItemID, ContainerID: r.ContainerID, ContentTypeID: r.ContentTypeID, SchemaRevisionID: r.SchemaRevisionID, BlobID: r.BlobID, RevisionNumber: r.RevisionNumber, Name: r.Name, Tags: nonNil(r.Tags), Payload: JSONObject(r.Payload), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
	if s := r.Edges.SchemaRevision; s != nil {
		schema, err := schemaRevisionOut(s)
		if err != nil {
			return out, err
		}
		out.SchemaRevision = &schema
	}
	return out, nil
}

type SchemaRevision struct {
	ID             string               `json:"id" format:"uuid"`
	ContainerID    string               `json:"container_id" format:"uuid"`
	RevisionNumber int                  `json:"revision_number" minimum:"1"`
	Definition     dms.SchemaDefinition `json:"definition" doc:"Frozen collection field catalog, content types and rules. Each item revision uses its immutable content_type_id within this snapshot."`
	CreatedBy      string               `json:"created_by"`
	CreatedAt      time.Time            `json:"created_at"`
}

func schemaRevisionOut(s *ent.SchemaRevision) (SchemaRevision, error) {
	definition, err := schemaDefinition(s.Definition)
	return SchemaRevision{ID: s.ID, ContainerID: s.ContainerID, RevisionNumber: s.RevisionNumber, Definition: definition, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt}, err
}

// schemaDefinition decodes a stored definition with empty lists instead of nulls.
func schemaDefinition(raw []byte) (dms.SchemaDefinition, error) {
	var d dms.SchemaDefinition
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, err
	}
	d.Fields = schemaFields(d.Fields)
	if d.ContentTypes == nil {
		d.ContentTypes = []dms.SchemaContentType{}
	}
	for i := range d.ContentTypes {
		d.ContentTypes[i].FieldKeys = nonNil(d.ContentTypes[i].FieldKeys)
		d.ContentTypes[i].Rules = rules(d.ContentTypes[i].Rules)
	}
	return d, nil
}
func schemaFields(fields []dms.SchemaField) []dms.SchemaField {
	if fields == nil {
		return []dms.SchemaField{}
	}
	for i := range fields {
		fields[i].Choices = nonNil(fields[i].Choices)
	}
	return fields
}

type SchemaTemplate struct {
	ID          string                 `json:"id" format:"uuid"`
	WorkspaceID string                 `json:"workspace_id" format:"uuid"`
	Key         string                 `json:"key"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Fields      []dms.SchemaField      `json:"fields"`
	Rules       []model.ValidationRule `json:"rules"`
	Version     int                    `json:"version" minimum:"1"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

func templateOut(t *ent.SchemaTemplate) (SchemaTemplate, error) {
	d, err := schemaDefinition(t.Definition)
	return SchemaTemplate{ID: t.ID, WorkspaceID: t.WorkspaceID, Key: t.Key, Name: t.Name, Description: t.Description, Fields: d.Fields, Rules: rules(d.Rules), Version: t.Version, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}, err
}

type TermSet struct {
	ID          string    `json:"id" format:"uuid"`
	WorkspaceID string    `json:"workspace_id" format:"uuid"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     int       `json:"version" minimum:"1"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func termSetOut(t *ent.TermSet) (TermSet, error) {
	return TermSet{ID: t.ID, WorkspaceID: t.WorkspaceID, Key: t.Key, Name: t.Name, Description: t.Description, Version: t.Version, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}, nil
}

type Term struct {
	ID             string            `json:"id" format:"uuid"`
	TermSetID      string            `json:"term_set_id" format:"uuid"`
	ParentID       *string           `json:"parent_id,omitempty" format:"uuid" doc:"Absent for root terms."`
	Name           string            `json:"name"`
	NormalizedName string            `json:"normalized_name"`
	Labels         map[string]string `json:"labels" doc:"Localized labels by language tag."`
	Synonyms       []string          `json:"synonyms"`
	Deprecated     bool              `json:"deprecated"`
	Version        int               `json:"version" minimum:"1"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func termOut(t *ent.Term) (Term, error) {
	labels := t.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	return Term{ID: t.ID, TermSetID: t.TermSetID, ParentID: t.ParentID, Name: t.Name, NormalizedName: t.NormalizedName, Labels: labels, Synonyms: nonNil(t.Synonyms), Deprecated: t.Deprecated, Version: t.Version, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}, nil
}

type RelationshipType struct {
	ID           string            `json:"id" format:"uuid"`
	WorkspaceID  string            `json:"workspace_id" format:"uuid"`
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	InverseLabel string            `json:"inverse_label"`
	Directed     bool              `json:"directed"`
	MaxIncoming  *int              `json:"max_incoming,omitempty" minimum:"1" maximum:"1000"`
	MaxOutgoing  *int              `json:"max_outgoing,omitempty" minimum:"1" maximum:"1000"`
	Attributes   []dms.SchemaField `json:"attributes" doc:"Attribute definitions validating relationship metadata."`
	Version      int               `json:"version" minimum:"1"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

func relationshipTypeOut(t *ent.RelationshipType) (RelationshipType, error) {
	d, err := schemaDefinition(t.Attributes)
	return RelationshipType{ID: t.ID, WorkspaceID: t.WorkspaceID, Key: t.Key, Label: t.Label, InverseLabel: t.InverseLabel, Directed: t.Directed, MaxIncoming: t.MaxIncoming, MaxOutgoing: t.MaxOutgoing, Attributes: d.Fields, Version: t.Version, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}, err
}

type ListView struct {
	ID          string        `json:"id" format:"uuid"`
	ContainerID string        `json:"container_id" format:"uuid"`
	Name        string        `json:"name"`
	Columns     []string      `json:"columns"`
	Query       dms.QuerySpec `json:"query"`
	Layout      string        `json:"layout" enum:"table,board,calendar,gallery"`
	IsDefault   bool          `json:"is_default"`
	Version     int           `json:"version" minimum:"1"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

func viewOut(v *ent.ListView) (ListView, error) {
	out := ListView{ID: v.ID, ContainerID: v.ContainerID, Name: v.Name, Columns: nonNil(v.Columns), Layout: v.Layout, IsDefault: v.IsDefault, Version: v.Version, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if len(v.Query) == 0 {
		return out, nil
	}
	return out, json.Unmarshal(v.Query, &out.Query)
}

type ContentType struct {
	ID          string                 `json:"id" format:"uuid"`
	ContainerID string                 `json:"container_id" format:"uuid"`
	Key         string                 `json:"key"`
	Name        string                 `json:"name"`
	FieldKeys   []string               `json:"field_keys"`
	Rules       []model.ValidationRule `json:"rules"`
	IsDefault   bool                   `json:"is_default"`
	Version     int                    `json:"version" minimum:"1"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

func contentTypeOut(t *ent.ContentType) (ContentType, error) {
	return ContentType{ID: t.ID, ContainerID: t.ContainerID, Key: t.Key, Name: t.Name, FieldKeys: nonNil(t.FieldKeys), Rules: rules(t.Rules), IsDefault: t.IsDefault, Version: t.Version, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}, nil
}

type WebDAVCredential struct {
	ID        string     `json:"id" format:"uuid"`
	Subject   string     `json:"subject"`
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"Absent for credentials that last until revoked."`
	CreatedAt time.Time  `json:"created_at"`
}

func credentialOut(c *ent.WebDAVCredential) WebDAVCredential {
	return WebDAVCredential{ID: c.ID, Subject: c.Subject, Label: c.Label, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt}
}

type NewWebDAVCredential struct {
	WebDAVCredential
	Password string `json:"password" doc:"Shown once. Use it as the HTTP Basic password for /webdav/ with any user name."`
}

type QueryResult struct {
	Data       []Resource `json:"data"`
	NextCursor string     `json:"next_cursor,omitempty" doc:"Opaque cursor for the next page; absent on the last page."`
	Total      int        `json:"total" minimum:"0" doc:"Authorized matches before pagination."`
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
func rules(v []model.ValidationRule) []model.ValidationRule {
	if v == nil {
		return []model.ValidationRule{}
	}
	return v
}

// convert maps a slice with a fallible converter, always returning a non-nil slice.
func convert[T, U any](in []T, fn func(T) (U, error)) ([]U, error) {
	out := make([]U, 0, len(in))
	for _, v := range in {
		u, err := fn(v)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// infallible adapts a plain converter for convert.
func infallible[T, U any](fn func(T) U) func(T) (U, error) {
	return func(v T) (U, error) { return fn(v), nil }
}
