package dms

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/grant"
	"papergo/ent/resource"
	"papergo/internal/identity"
	"papergo/internal/model"
	"strings"
	"time"
)

type Service struct {
	Client      *ent.Client
	db          *sql.DB
	writeMu     chan struct{}
	transaction bool
	// operations wakes RunOperations when work is queued.
	operations          chan struct{}
	onCommit            func()
	tx                  *sql.Tx
	pending             *[]Event
	collectionTemplates map[string]CollectionTemplate
}

// NewService serves the PaperGo database db.
func NewService(db *sql.DB) *Service {
	return &Service{Client: ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db))), db: db, writeMu: make(chan struct{}, 1), operations: make(chan struct{}, 1), collectionTemplates: builtInCollectionTemplates()}
}

// OnCommit calls f after each committed write that recorded domain events,
// so a dispatcher can pick them up at once. Call it before the service is used.
func (s *Service) OnCommit(f func()) { s.onCommit = f }

// txDriver runs Ent on a transaction the service does not own. Ent opens its
// own transaction for multi-statement saves; here those join tx instead.
type txDriver struct{ *entsql.Driver }

func (d txDriver) Tx(context.Context) (dialect.Tx, error) { return dialect.NopTx(d.Driver), nil }

func entClient(tx *sql.Tx) *ent.Client {
	return ent.NewClient(ent.Driver(txDriver{entsql.NewDriver(dialect.SQLite, entsql.Conn{ExecQuerier: tx})}))
}

// Mutations include authorization, version checks, audit records and the
// domain event log in one transaction. SQLite WAL allows concurrent readers; one writer
// per process keeps PaperGo writes in order, and immediate transactions make
// other writers (the runner) wait for the lock instead of failing.
// Waiting for the writer slot honors ctx, so a timed-out request leaves the queue.
func (s *Service) write(ctx context.Context, fn func(*Service) error) error {
	if s.transaction {
		return fn(s)
	}
	select {
	case s.writeMu <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.writeMu }()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	recorded, err := s.inTx(ctx, tx, fn)
	if err != nil {
		_ = tx.Rollback()
	} else {
		err = tx.Commit()
	}
	if ent.IsConstraintError(err) {
		return ErrConflict
	}
	if err == nil && recorded > 0 && s.onCommit != nil {
		s.onCommit()
	}
	return err
}

// inTx runs fn on a service bound to tx and then records the events fn
// raised in the domain event log, so they commit or roll back with its
// writes. It returns how many events it recorded.
func (s *Service) inTx(ctx context.Context, tx *sql.Tx, fn func(*Service) error) (int, error) {
	var pending []Event
	t := &Service{Client: entClient(tx), db: s.db, writeMu: s.writeMu, transaction: true, operations: s.operations, tx: tx, pending: &pending, collectionTemplates: s.collectionTemplates}
	if err := fn(t); err != nil {
		return 0, err
	}
	return len(pending), recordEvents(ctx, tx, pending)
}

// WriteTx runs fn as one write inside tx, which the caller owns and commits.
// The runner uses it so a workflow step's writes, their events and the step's
// completion marker commit together; it wakes the dispatcher itself.
func (s *Service) WriteTx(ctx context.Context, tx *sql.Tx, fn func(*Service) error) error {
	_, err := s.inTx(ctx, tx, fn)
	return err
}

// A consistent snapshot binds authorization, surface selection and hydration.
func read[T any](ctx context.Context, s *Service, fn func(*Service) (T, error)) (out T, err error) {
	if s.transaction {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = fn(&Service{Client: entClient(tx), db: s.db, writeMu: s.writeMu, transaction: true, operations: s.operations, tx: tx, collectionTemplates: s.collectionTemplates})
	if err != nil {
		return out, err
	}
	err = tx.Commit()
	return out, err
}
func (s *Service) audit(ctx context.Context, subject, action string, r *ent.Resource, details map[string]any) error {
	_, err := s.Client.AuditEvent.Create().SetWorkspaceID(r.WorkspaceID).SetResourceID(r.ID).SetSubject(subject).SetAction(action).SetDetails(details).Save(ctx)
	return err
}
func (s *Service) Get(ctx context.Context, subject, id string) (*ent.Resource, error) {
	return s.GetSurface(ctx, subject, id, "auto")
}

type CreateResource struct {
	DuplicatePolicy   *string        `json:"duplicate_policy,omitempty"`
	ID                *string        `json:"id,omitempty"`
	TemplateKey       string         `json:"template_key,omitempty"`
	ContentTypeID     string         `json:"content_type_id,omitempty"`
	ContentTypeIDs    []string       `json:"content_type_ids,omitempty"`
	Kind              string         `json:"kind"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	AllowFolders      *bool          `json:"allow_folders,omitempty"`
	Tags              []string       `json:"tags"`
	Values            map[string]any `json:"values"`
	Versioning        *string        `json:"versioning,omitempty"`
	MaxVersions       *int           `json:"max_versions,omitempty"`
	PublishingEnabled bool           `json:"publishing_enabled"`
	WebDAVEnabled     bool           `json:"webdav_enabled"`
}

func (s *Service) Create(ctx context.Context, subject, parentID string, in CreateResource) (out *ent.Resource, err error) {
	out, _, err = s.CreateWithStatus(ctx, subject, parentID, in)
	return
}

// create inserts a resource inside a write transaction. A library item may be
// created with its first blob, so its first revision already carries content.
func (s *Service) create(ctx context.Context, subject, parentID string, in CreateResource, content *BlobInput) (*ent.Resource, error) {
	var template *CollectionTemplate
	if in.TemplateKey != "" {
		tpl, ok := s.collectionTemplates[in.TemplateKey]
		if !ok {
			return nil, invalid("unknown collection template")
		}
		if in.Kind != "" && in.Kind != "list" && in.Kind != "library" {
			return nil, invalid("template_key belongs to collections")
		}
		if len(in.ContentTypeIDs) > 0 {
			return nil, invalid("template_key and content_type_ids are mutually exclusive")
		}
		template = &tpl
		in.Kind, in.AllowFolders = tpl.Kind, &tpl.AllowFolders
		if in.Versioning == nil {
			in.Versioning = &tpl.Versioning
		}
		if in.MaxVersions == nil {
			in.MaxVersions = &tpl.MaxVersions
		}
		in.PublishingEnabled, in.WebDAVEnabled = tpl.PublishingEnabled, tpl.WebDAVEnabled
		if in.DuplicatePolicy == nil && tpl.DuplicatePolicy != "" {
			in.DuplicatePolicy = &tpl.DuplicatePolicy
		}
	}
	if err := validateDuplicatePolicy(in.Kind, in.DuplicatePolicy); err != nil {
		return nil, err
	}
	collection := in.Kind == "list" || in.Kind == "library"
	if err := validateHistorySettings(collection, in.Versioning, in.MaxVersions); err != nil {
		return nil, err
	}
	versioning := "off"
	if in.Kind == "library" {
		versioning = "major"
	}
	if in.Versioning != nil {
		versioning = *in.Versioning
	}
	scope := "list.write"
	if in.Kind == "workspace" {
		scope = "workspace.create"
	} else if collection {
		scope = "list.read"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	if in.Kind == "workspace" || collection {
		in.Name = strings.TrimSpace(in.Name)
		if e := validateContainerMetadata(in.Name, in.Description); e != nil {
			return nil, e
		}
	} else if in.Description != "" {
		return nil, invalid("description belongs to workspaces, lists and libraries")
	}
	if in.AllowFolders != nil && !collection {
		return nil, invalid("allow_folders belongs to lists and libraries")
	}
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	if in.Values == nil {
		in.Values = map[string]any{}
	}
	if parentID == "" && in.Kind != "workspace" {
		return nil, invalid("root resource must be a workspace")
	}
	if in.PublishingEnabled && in.Kind != "list" && in.Kind != "library" {
		return nil, invalid("publishing_enabled belongs to lists and libraries")
	}
	if in.WebDAVEnabled && in.Kind != "library" {
		return nil, invalid("webdav_enabled belongs to libraries")
	}
	if content != nil && in.Kind != "item" {
		return nil, invalid("only items have content")
	}
	if in.ContentTypeID != "" && in.Kind != "item" && in.Kind != "folder" {
		return nil, invalid("content_type_id belongs to items and folders")
	}
	if len(in.ContentTypeIDs) > 0 && !collection {
		return nil, invalid("content_type_ids belongs to collections")
	}
	if len(in.ContentTypeIDs) > 32 {
		return nil, invalid("at most 32 content types per collection")
	}
	id := uuid.NewString()
	if in.ID != nil {
		id = *in.ID
	}
	workspaceID := id
	var parent *ent.Resource
	var containerID *string
	if parentID != "" {
		var e error
		parent, e = s.authorize(ctx, subject, parentID, "write")
		if e != nil {
			return nil, e
		}
		workspaceID = parent.WorkspaceID
		valid := false
		switch string(parent.Kind) {
		case "workspace":
			valid = in.Kind == "list" || in.Kind == "library"
			if valid {
				if _, e = s.authorize(ctx, subject, parentID, "manage"); e != nil {
					return nil, e
				}
			}
		case "list", "library":
			valid = in.Kind == "folder" || in.Kind == "item"
			v := parent.ID
			containerID = &v
		case "folder":
			valid = in.Kind == "folder" || in.Kind == "item"
			containerID = parent.ContainerID
		}
		if !valid {
			return nil, invalid("invalid parent for resource kind")
		}
		depth, e := s.depth(ctx, parent)
		if e != nil {
			return nil, e
		}
		if depth >= MaxDepth {
			return nil, invalid("maximum hierarchy depth reached")
		}
	}
	if in.Kind == "folder" && containerID != nil {
		c, err := s.Client.Resource.Get(ctx, *containerID)
		if err != nil {
			return nil, err
		}
		if !c.AllowFolders {
			return nil, invalid("this collection does not allow new folders")
		}
	}
	key, e := s.fileNameKey(ctx, resource.Kind(in.Kind), containerID, in.Name)
	if e != nil {
		return nil, e
	}
	var typeID *string
	if in.Kind == "item" || in.Kind == "folder" {
		if containerID == nil {
			return nil, invalid("item must belong to a list or library")
		}
		defs, e := s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(*containerID)).All(ctx)
		if e != nil {
			return nil, e
		}
		typ, e := s.contentType(ctx, *containerID, in.ContentTypeID)
		if e != nil {
			return nil, e
		}
		typeID = &typ.ID
		selected, _ := resourceDefinitions(resource.Kind(in.Kind), selectTypeDefinitions(defs, typ), nil)
		if e = normalizeRevisionValues(selected, in.Values, nil, in.Kind == "item"); e != nil {
			return nil, e
		}
	} else if len(in.Values) > 0 {
		return nil, invalid("custom values are supported on items and folders only")
	}
	builder := s.Client.Resource.Create().SetID(id).SetWorkspaceID(workspaceID).SetKind(resource.Kind(in.Kind)).SetName(in.Name).SetTags(in.Tags).SetValues(in.Values).SetNillableContainerID(containerID).SetPublishingEnabled(in.PublishingEnabled).SetWebdavEnabled(in.WebDAVEnabled).SetNillableNameKey(key)
	if template != nil {
		if e = s.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		in.ContentTypeIDs, e = s.provisionCollectionTypes(ctx, subject, *template)
		if e != nil {
			return nil, e
		}
		builder.SetTemplateKeys([]string{template.Key})
	}
	builder.SetCreatedBy(subject).SetUpdatedBy(subject)
	builder.SetVersioning(resource.Versioning(versioning)).SetNillableMaxVersions(in.MaxVersions)
	if in.DuplicatePolicy != nil {
		builder.SetDuplicatePolicy(resource.DuplicatePolicy(*in.DuplicatePolicy))
	}
	builder.SetDescription(in.Description)
	if in.AllowFolders != nil {
		builder.SetAllowFolders(*in.AllowFolders)
	}
	builder.SetNillableContentTypeID(typeID)
	if parent != nil {
		builder.SetParentID(parent.ID)
	} else {
		builder.SetInheritPermissions(false)
	}
	out, e := builder.Save(ctx)
	if e != nil {
		return nil, e
	}
	if parent == nil {
		_, e = s.Client.Grant.Create().SetResourceID(out.ID).SetPrincipalType(grant.PrincipalTypeUser).SetPrincipalID(subject).SetAction(grant.ActionManage).SetEffect(grant.EffectAllow).Save(ctx)
		if e != nil {
			return nil, e
		}
	}
	if out.Kind == resource.KindList || out.Kind == resource.KindLibrary {
		ids := in.ContentTypeIDs
		if len(ids) == 0 {
			typ, e := s.Client.ContentType.Create().SetKey("item").SetName("Item").Save(ctx)
			if e != nil {
				return nil, e
			}
			ids = []string{typ.ID}
		} else if e = s.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		for i, id := range ids {
			if _, e = s.Client.ContentType.Get(ctx, id); ent.IsNotFound(e) {
				return nil, ErrNotFound
			} else if e != nil {
				return nil, e
			}
			if e = s.Client.CollectionType.Create().SetContainerID(out.ID).SetContentTypeID(id).SetIsDefault(i == 0).Exec(ctx); e != nil {
				return nil, e
			}
		}
		if e = s.refreshCollections(ctx, subject, []*ent.Resource{out}); e != nil {
			return nil, e
		}
	}
	if template != nil {
		for _, view := range template.Views {
			if _, e = s.CreateView(ctx, subject, out.ID, view); e != nil {
				return nil, e
			}
		}
	}
	details := map[string]any{"kind": in.Kind}
	if contentKind(out.Kind) {
		var blobID *string
		if content != nil {
			b, e := s.createBlob(ctx, subject, out.ID, *content, true)
			if e != nil {
				return nil, e
			}
			blobID = &b.ID
			details["blob_id"] = b.ID
		}
		if _, e = s.recordRevision(ctx, subject, out, blobID, false); e != nil {
			return nil, e
		}
	}
	out, e = s.Client.Resource.Get(ctx, out.ID)
	if e != nil {
		return nil, e
	}
	if contentKind(out.Kind) {
		if e = s.overlayHead(ctx, out); e != nil {
			return nil, e
		}
	}
	return out, s.audit(ctx, subject, "resource.create", out, details)
}

// depth counts r and its ancestors.
func (s *Service) depth(ctx context.Context, r *ent.Resource) (int, error) {
	depth := 1
	for ; r.ParentID != nil; depth++ {
		if depth > MaxDepth {
			return depth, invalid("invalid hierarchy depth")
		}
		var err error
		if r, err = s.Client.Resource.Get(ctx, *r.ParentID); err != nil {
			return depth, err
		}
	}
	return depth, nil
}

type UpdateResource struct {
	DuplicatePolicy   *string         `json:"duplicate_policy,omitempty"`
	Name              *string         `json:"name,omitempty"`
	Description       *string         `json:"description,omitempty"`
	AllowFolders      *bool           `json:"allow_folders,omitempty"`
	Tags              *[]string       `json:"tags,omitempty"`
	Values            *map[string]any `json:"values,omitempty"`
	ContentTypeID     *string         `json:"content_type_id,omitempty"`
	Versioning        *string         `json:"versioning,omitempty"`
	MaxVersions       *int            `json:"max_versions,omitempty"`
	PublishingEnabled *bool           `json:"publishing_enabled,omitempty"`
	WebDAVEnabled     *bool           `json:"webdav_enabled,omitempty"`
	// ParentID moves a folder or item to another folder of the same collection.
	ParentID *string `json:"parent_id,omitempty"`
}

func (s *Service) Update(ctx context.Context, subject, id string, version int, in UpdateResource) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.update(ctx, subject, id, version, in)
		return e
	})
	return
}
func (s *Service) update(ctx context.Context, subject, id string, version int, in UpdateResource) (*ent.Resource, error) {
	content := in.Name != nil || in.Description != nil || in.Tags != nil || in.Values != nil || in.ContentTypeID != nil
	if !content && in.DuplicatePolicy == nil && in.PublishingEnabled == nil && in.WebDAVEnabled == nil && in.AllowFolders == nil && in.ParentID == nil && in.Versioning == nil && in.MaxVersions == nil {
		return nil, invalid("at least one change is required")
	}
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return nil, err
		}
	}
	if in.Tags != nil {
		if err := validateTags(*in.Tags); err != nil {
			return nil, err
		}
	}
	r, e := s.authorize(ctx, subject, id, "write")
	if e != nil {
		return nil, e
	}
	if err := validateDuplicatePolicy(string(r.Kind), in.DuplicatePolicy); err != nil {
		return nil, err
	}
	collection := r.Kind == resource.KindList || r.Kind == resource.KindLibrary
	if err := validateHistorySettings(collection, in.Versioning, in.MaxVersions); err != nil {
		return nil, err
	}
	if r.Kind == resource.KindWorkspace || collection {
		if _, e = s.authorize(ctx, subject, id, "manage"); e != nil {
			return nil, e
		}
		name, description := r.Name, r.Description
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
			in.Name = &name
		}
		if in.Description != nil {
			description = *in.Description
		}
		if e = validateContainerMetadata(name, description); e != nil {
			return nil, e
		}
	} else if in.Description != nil {
		return nil, invalid("description belongs to workspaces, lists and libraries")
	}
	if in.AllowFolders != nil && !collection {
		return nil, invalid("allow_folders belongs to lists and libraries")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	if in.PublishingEnabled != nil || in.WebDAVEnabled != nil {
		if in.PublishingEnabled != nil && r.Kind != resource.KindList && r.Kind != resource.KindLibrary {
			return nil, invalid("publishing_enabled belongs to lists and libraries")
		}
		if in.WebDAVEnabled != nil && r.Kind != resource.KindLibrary {
			return nil, invalid("webdav_enabled belongs to libraries")
		}
		if _, e = s.authorize(ctx, subject, id, "manage"); e != nil {
			return nil, e
		}
	}
	if contentKind(r.Kind) {
		if e = s.overlayHead(ctx, r); e != nil {
			return nil, e
		}
	}
	if in.Values != nil && (!contentKind(r.Kind) || r.ContainerID == nil) {
		return nil, invalid("only items and folders support custom values")
	}
	if in.ContentTypeID != nil && (!contentKind(r.Kind) || *in.ContentTypeID == "") {
		return nil, invalid("content_type_id requires an attached content type")
	}
	moved := in.ParentID != nil && (r.ParentID == nil || *in.ParentID != *r.ParentID)
	b := s.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).AddVersion(1).SetUpdatedAt(time.Now().UTC())
	b.SetUpdatedBy(subject)
	if in.Description != nil {
		b.SetDescription(*in.Description)
	}
	if in.AllowFolders != nil {
		b.SetAllowFolders(*in.AllowFolders)
	}
	if moved {
		if e = s.checkMove(ctx, subject, r, *in.ParentID); e != nil {
			return nil, e
		}
		b.SetParentID(*in.ParentID)
	}
	if in.Name != nil {
		key, e := s.fileNameKey(ctx, r.Kind, r.ContainerID, *in.Name)
		if e != nil {
			return nil, e
		}
		b.SetName(*in.Name).SetNillableNameKey(key)
	}
	// Item values are validated and normalized once, by recordRevision below.
	if contentKind(r.Kind) {
		if in.ContentTypeID != nil && *in.ContentTypeID != *r.ContentTypeID {
			c, err := s.Client.Resource.Get(ctx, *r.ContainerID)
			if err != nil {
				return nil, err
			}
			schema, err := s.Client.SchemaRevision.Get(ctx, *c.SchemaHeadID)
			if err != nil {
				return nil, err
			}
			defs, _, err := typedDefinitions(schema.Definition, *in.ContentTypeID)
			if err != nil {
				return nil, err
			}
			known := definitionMap(defs)
			for key := range r.Values {
				if known[key] == nil {
					delete(r.Values, key)
				}
			}
			r.ContentTypeID = in.ContentTypeID
			b.SetContentTypeID(*in.ContentTypeID)
		}
		if in.Name != nil {
			r.Name = *in.Name
		}
		if in.Tags != nil {
			r.Tags = *in.Tags
		}
		if in.Values != nil {
			// Prune fields removed from the live schema before applying this patch.
			c, err := s.Client.Resource.Get(ctx, *r.ContainerID)
			if err != nil {
				return nil, err
			}
			schema, err := s.Client.SchemaRevision.Get(ctx, *c.SchemaHeadID)
			if err != nil {
				return nil, err
			}
			defs, _, err := typedDefinitions(schema.Definition, *r.ContentTypeID)
			if err != nil {
				return nil, err
			}
			known := definitionMap(defs)
			for key := range *in.Values {
				if known[key] == nil {
					return nil, invalid("unknown field for this content type: " + key)
				}
				// An explicit lookup/person assignment validates every supplied target,
				// even a retained ID. Omitted references survive unrelated edits.
				if d := known[key]; (d.Type == fielddefinition.TypeLookup || d.Type == fielddefinition.TypePerson) && (*in.Values)[key] != nil {
					assigned := map[string]any{key: (*in.Values)[key]}
					if err = s.normalizeItemValues(ctx, subject, []*ent.FieldDefinition{d}, assigned, nil, true); err != nil {
						return nil, err
					}
					(*in.Values)[key] = assigned[key]
				}
			}
			current := map[string]any{}
			for key, value := range r.Values {
				if known[key] != nil {
					current[key] = value
				}
			}
			r.Values = patchValues(current, *in.Values)
		}
		b.SetValues(r.Values)
	}
	if in.Tags != nil {
		b.SetTags(*in.Tags)
	}
	if in.Versioning != nil {
		b.SetVersioning(resource.Versioning(*in.Versioning))
	}
	if in.DuplicatePolicy != nil {
		b.SetDuplicatePolicy(resource.DuplicatePolicy(*in.DuplicatePolicy))
	}
	if in.MaxVersions != nil {
		b.SetMaxVersions(*in.MaxVersions)
	}
	if in.PublishingEnabled != nil {
		b.SetPublishingEnabled(*in.PublishingEnabled)
	}
	if in.WebDAVEnabled != nil {
		b.SetWebdavEnabled(*in.WebDAVEnabled)
	}
	n, e := b.Save(ctx)
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, ErrPreconditionFailed
	}
	out, e := s.Client.Resource.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if contentKind(out.Kind) {
		// A move alone changes location, not content, so it records no revision.
		if content {
			out.Values = r.Values
			if _, e = s.recordRevision(ctx, subject, out, nil, in.Values == nil); e != nil {
				return nil, e
			}
			if out, e = s.Client.Resource.Get(ctx, id); e != nil {
				return nil, e
			}
		}
		if e = s.overlayHead(ctx, out); e != nil {
			return nil, e
		}
	}
	if in.PublishingEnabled != nil && !*in.PublishingEnabled {
		if e = s.publishAllHeads(ctx, subject, out); e != nil {
			return nil, e
		}
	}
	details := map[string]any{"version": out.Version}
	if moved {
		details["from_parent_id"] = *r.ParentID
		details["parent_id"] = *in.ParentID
	}
	return out, s.audit(ctx, subject, "resource.update", out, details)
}

// checkMove validates moving r below parentID: both ends need write access,
// the parent must belong to r's collection, and the subtree must stay within
// MaxDepth without entering itself. Triggers repeat the containment rules.
func (s *Service) checkMove(ctx context.Context, subject string, r *ent.Resource, parentID string) error {
	if r.Kind != resource.KindFolder && r.Kind != resource.KindItem {
		return invalid("only folders and items can move")
	}
	p, err := s.authorize(ctx, subject, parentID, "write")
	if err != nil {
		return err
	}
	if r.ContainerID == nil || (p.ID != *r.ContainerID && (p.Kind != resource.KindFolder || p.ContainerID == nil || *p.ContainerID != *r.ContainerID)) {
		return invalid("folders and items move only within their collection")
	}
	// One walk up from the new parent both rejects entering r and counts depth.
	depth := 1
	for a := p; a.ParentID != nil; depth++ {
		if a.ID == r.ID {
			return invalid("a folder cannot move into itself")
		}
		if depth > MaxDepth {
			return invalid("invalid hierarchy depth")
		}
		if a, err = s.Client.Resource.Get(ctx, *a.ParentID); err != nil {
			return err
		}
	}
	height, err := s.subtreeHeight(ctx, r.ID)
	if err != nil {
		return err
	}
	if depth+height > MaxDepth {
		return invalid("maximum hierarchy depth reached")
	}
	return nil
}

// subtreeHeight counts the levels of id and its live descendants.
func (s *Service) subtreeHeight(ctx context.Context, id string) (int, error) {
	var height int
	err := s.scan(ctx, []any{&height}, `WITH RECURSIVE sub(id,depth) AS (SELECT ?,1 UNION ALL SELECT c.id,sub.depth+1 FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.deleted_at IS NULL AND sub.depth<=?) SELECT max(depth) FROM sub`, id, MaxDepth)
	return height, err
}

// scan reads the single row of an aggregate query.
func (s *Service) scan(ctx context.Context, dest []any, query string, args ...any) error {
	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return err
	}
	if err = rows.Scan(dest...); err != nil {
		return err
	}
	return rows.Close()
}

type DeleteOptions struct {
	Recursive bool
}

// Delete removes an item or empty folder, or a folder tree when Recursive is
// explicit. Workspace and collection management deletes their full trees.
// Revisions, publications and audit history remain for recycle-bin recovery;
// derived read projections are removed and relationships are hidden until restore.
func (s *Service) Delete(ctx context.Context, subject, id string, version int, options DeleteOptions) error {
	return s.write(ctx, func(t *Service) error { return t.delete(ctx, subject, id, version, options.Recursive) })
}
func (s *Service) delete(ctx context.Context, subject, id string, version int, recursive bool) error {
	r, err := s.authorize(ctx, subject, id, "write")
	if err != nil {
		return err
	}
	collection := r.Kind == resource.KindList || r.Kind == resource.KindLibrary
	if r.Kind == resource.KindWorkspace || collection {
		if _, err = s.authorize(ctx, subject, id, "manage"); err != nil {
			return err
		}
	}
	if r.Version != version {
		return ErrPreconditionFailed
	}
	if r.Kind == resource.KindFolder && !recursive {
		populated, err := s.Client.Resource.Query().Where(resource.ParentIDEQ(id), resource.DeletedAtIsNil()).Exist(ctx)
		if err != nil {
			return err
		}
		if populated {
			return ErrFolderNotEmpty
		}
	}
	subtree := `WITH RECURSIVE sub(id) AS (SELECT ? UNION ALL SELECT c.id FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.deleted_at IS NULL) SELECT id FROM sub`
	writable, args := permissionSQL("r.id", subject, "write")
	var total, allowed int
	if err = s.scan(ctx, []any{&total, &allowed}, `SELECT count(*), coalesce(sum(CASE WHEN `+writable+` THEN 1 ELSE 0 END),0) FROM resources r WHERE r.id IN (`+subtree+`)`, append(args, id)...); err != nil {
		return err
	}
	// Explicit recursion authorizes every descendant before removing any content.
	if !collection && allowed != total {
		return ErrForbidden
	}
	for _, statement := range []string{
		`DELETE FROM field_values WHERE item_id IN (` + subtree + `)`,
		`DELETE FROM business_keys WHERE item_id IN (` + subtree + `)`,
		`DELETE FROM item_surfaces WHERE item_id IN (` + subtree + `)`,
	} {
		if _, err = s.Client.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	var links int
	if err = s.scan(ctx, []any{&links}, `SELECT count(*) FROM relationships WHERE source_id IN (`+subtree+`) OR target_id IN (`+subtree+`)`, id, id); err != nil {
		return err
	}
	if s.pending != nil {
		items, err := s.Client.Resource.Query().Where(resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil(), func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP(sel.C(resource.FieldID)+" IN ("+subtree+")", id))
		}).Select(resource.FieldID, resource.FieldWorkspaceID, resource.FieldContainerID).All(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			s.emit(ctx, EventItemDeleted, subject, item.WorkspaceID, item.ContainerID, item.ID, nil)
		}
	}
	now := time.Now().UTC()
	if r.Kind == resource.KindWorkspace {
		// Schedules and event dispatch only select enabled, live workflows.
		if _, err = s.Client.ExecContext(ctx, `UPDATE workflows SET enabled=0, deleted_at=?, updated_at=?, updated_by=?, version=version+1 WHERE workspace_id=? AND deleted_at IS NULL`, now, now, subject, id); err != nil {
			return err
		}
	}
	if collection {
		if _, err = s.Client.ExecContext(ctx, `UPDATE workflows SET enabled=0, deleted_at=?, updated_at=?, updated_by=?, version=version+1 WHERE deleted_at IS NULL AND (collection_id=? OR id IN (SELECT workflow_id FROM workflow_triggers WHERE collection_id=?))`, now, now, subject, id, id); err != nil {
			return err
		}
	}
	if _, err = s.Client.ExecContext(ctx, `UPDATE resources SET deleted_at=?, deleted_by=?, name_key=NULL, version=version+1, updated_at=?, updated_by=? WHERE id IN (`+subtree+`)`, now, subject, now, subject, id); err != nil {
		return err
	}
	return s.audit(ctx, subject, "resource.delete", r, map[string]any{"version": version + 1, "descendants": total - 1, "relationships": links})
}

// fileNameKey is the unique, case-insensitive name of a library folder or item,
// and nil for other resources. Library names must be usable as file names.
func (s *Service) fileNameKey(ctx context.Context, kind resource.Kind, containerID *string, name string) (*string, error) {
	if (kind != resource.KindFolder && kind != resource.KindItem) || containerID == nil {
		return nil, nil
	}
	c, err := s.Client.Resource.Get(ctx, *containerID)
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindLibrary {
		return nil, nil
	}
	if !filenameValid(name) {
		return nil, invalid("library folder and file names cannot contain / \\ or control characters")
	}
	key := nameKey(name)
	return &key, nil
}

// nameKey folds case like Windows and macOS file systems, which treat names
// differing only in case as the same file. It matches SQL unicode_lower.
func nameKey(name string) string { return strings.ToLower(name) }

type CreateField struct {
	ContentTypeID string             `json:"content_type_id,omitempty"`
	Options       model.FieldOptions `json:"options,omitempty"`
	Key           string             `json:"key"`
	Label         string             `json:"label"`
	Type          string             `json:"type"`
	Required      bool               `json:"required"`
	Choices       []string           `json:"choices"`
	Indexed       bool               `json:"indexed"`
	Scale         int                `json:"scale"`
}

func (s *Service) CreateField(ctx context.Context, subject, containerID string, in CreateField) (out *ent.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		out, e = t.createField(ctx, subject, c, in)
		if e != nil {
			return e
		}
		collections, e := t.fieldCollections(ctx, out.ID)
		if e != nil {
			return e
		}
		if e = t.refreshCollections(ctx, subject, collections); e != nil {
			return e
		}
		return t.audit(ctx, subject, "field.create", c, map[string]any{"key": in.Key})
	})
	return
}
func (s *Service) createField(ctx context.Context, subject string, c *ent.Resource, in CreateField) (*ent.FieldDefinition, error) {
	if c.Kind != "list" && c.Kind != "library" {
		return nil, invalid("fields belong to collections")
	}
	typ, e := s.contentType(ctx, c.ID, in.ContentTypeID)
	if e != nil {
		return nil, e
	}
	var used bool
	if e = s.scan(ctx, []any{&used}, "SELECT EXISTS(SELECT 1 FROM schema_revisions sr, json_each(sr.definition,'$.fields') f WHERE sr.container_id=? AND json_extract(f.value,'$.id')=?)", c.ID, in.Key); e != nil {
		return nil, e
	}
	if used {
		return nil, invalid("field keys cannot be reused; use a new key")
	}
	d := fieldFromInput(in)
	if d.Choices == nil {
		d.Choices = []string{}
	}
	if e := validateFieldDefinition(d); e != nil {
		return nil, e
	}
	if e := s.validateReferenceScopes(ctx, subject, d); e != nil {
		return nil, e
	}
	if len(d.Options.DefaultValue) > 0 {
		if e := s.normalizeItemValues(ctx, subject, []*ent.FieldDefinition{d}, map[string]any{}, nil, true); e != nil {
			return nil, e
		}
	}
	n, e := s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(c.ID)).Count(ctx)
	if e != nil {
		return nil, e
	}
	if n >= 200 {
		return nil, invalid("at most 200 fields per collection")
	}
	if d.Required && len(d.Options.DefaultValue) == 0 {
		exists, e := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.ContentTypeIDEQ(typ.ID), resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil()).Exist(ctx)
		if e != nil {
			return nil, e
		}
		if exists {
			return nil, invalid("required fields on populated collections need a default")
		}
	}
	out, e := s.Client.FieldDefinition.Create().SetKey(d.Key).SetLabel(d.Label).SetType(d.Type).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetScale(d.Scale).SetOptions(d.Options).Save(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.Client.ContentType.UpdateOne(typ).SetFieldIds(append(append([]string{}, typ.FieldIds...), out.ID)).AddVersion(1).Exec(ctx); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Fields(ctx context.Context, subject, id string) ([]*ent.FieldDefinition, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.FieldDefinition, error) { return t.Fields(ctx, subject, id) })
	}
	if _, err := s.authorize(ctx, subject, id, "read"); err != nil {
		return nil, err
	}
	return s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(id)).Order(ent.Asc(fielddefinition.FieldKey)).All(ctx)
}

type Permission struct {
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
	Action        string `json:"action"`
	Effect        string `json:"effect"`
}
type Permissions struct {
	CopyInherited   bool         `json:"copy_inherited,omitempty"`
	ScopeID         string       `json:"scope_id,omitempty"`
	EffectiveGrants []Permission `json:"effective_grants,omitempty"`
	Inherit         bool         `json:"inherit"`
	Grants          []Permission `json:"grants"`
}

func (s *Service) SetPermissions(ctx context.Context, subject, id string, version int, in Permissions) (out *ent.Resource, err error) {
	if in.Inherit && (len(in.Grants) > 0 || in.CopyInherited) {
		return nil, invalid("inherited resources cannot have local grants; create an exclusive scope first")
	}
	if len(in.Grants) > 200 {
		return nil, invalid("at most 200 grants per resource")
	}
	seen := map[string]bool{}
	for _, g := range in.Grants {
		if e := identity.ValidateSubject(g.PrincipalID); e != nil {
			return nil, invalid(e.Error())
		}
		if g.Action != "read" && g.Action != "read_draft" && g.Action != "write" && g.Action != "publish" && g.Action != "manage" {
			return nil, invalid("invalid permission action")
		}
		if g.Effect != "" && g.Effect != "allow" {
			return nil, invalid("only additive allow grants are supported")
		}
		key := g.PrincipalType + "\x00" + g.PrincipalID + "\x00" + g.Action
		if seen[key] {
			return nil, invalid("duplicate permission")
		}
		seen[key] = true
	}
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "manage")
		if e != nil {
			return e
		}
		if r.Version != version {
			return ErrPreconditionFailed
		}
		if r.Kind == resource.KindWorkspace {
			return invalid("workspace access is managed through members")
		}
		for _, g := range in.Grants {
			switch g.PrincipalType {
			case "user", "group":
				if e = identity.ValidatePrincipal(ctx, t.tx, identity.Principal{Type: g.PrincipalType, ID: g.PrincipalID}); e != nil {
					return invalid(e.Error())
				}
			case "workspace_visitor", "workspace_member", "workspace_owner":
				if g.PrincipalID != r.WorkspaceID {
					return invalid("workspace role principals must identify this resource's workspace")
				}
				if g.PrincipalType == "workspace_owner" && g.Action != "manage" {
					return invalid("workspace owners retain manage access")
				}
			default:
				return invalid("invalid permission principal_type")
			}
		}
		if in.CopyInherited {
			if !r.InheritPermissions {
				return invalid("copy_inherited requires an inherited resource")
			}
			_, copied, e := t.effectivePermissions(ctx, r)
			if e != nil {
				return e
			}
			merged := map[string]bool{}
			for _, g := range in.Grants {
				merged[g.PrincipalType+"\x00"+g.PrincipalID+"\x00"+g.Action] = true
			}
			for _, g := range copied {
				key := g.PrincipalType + "\x00" + g.PrincipalID + "\x00" + g.Action
				if !merged[key] {
					in.Grants = append(in.Grants, g)
					merged[key] = true
				}
			}
			if len(in.Grants) > 200 {
				return invalid("copied scope exceeds 200 grants")
			}
		}
		if _, e = t.Client.Grant.Delete().Where(grant.ResourceIDEQ(id)).Exec(ctx); e != nil {
			return e
		}
		n, e := t.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).SetInheritPermissions(in.Inherit).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrPreconditionFailed
		}
		for _, g := range in.Grants {
			if _, e = t.Client.Grant.Create().SetResourceID(id).SetPrincipalType(grant.PrincipalType(g.PrincipalType)).SetPrincipalID(g.PrincipalID).SetAction(grant.Action(g.Action)).SetEffect(grant.EffectAllow).Save(ctx); e != nil {
				return e
			}
		}
		out, e = t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if _, e = t.authorize(ctx, subject, id, "manage"); e != nil {
			return invalid("permission change would remove your manage access")
		}
		if contentKind(out.Kind) {
			if e = t.overlayHead(ctx, out); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "permissions.replace", out, map[string]any{"inherit": in.Inherit, "grant_count": len(in.Grants)})
	})
	return
}
func (s *Service) Permissions(ctx context.Context, subject, id string) (Permissions, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Permissions, error) { return t.Permissions(ctx, subject, id) })
	}
	r, err := s.authorize(ctx, subject, id, "manage")
	if err != nil {
		return Permissions{}, err
	}
	scope, effective, err := s.effectivePermissions(ctx, r)
	if err != nil {
		return Permissions{}, err
	}
	out := Permissions{Inherit: r.InheritPermissions, ScopeID: scope, EffectiveGrants: effective, Grants: []Permission{}}
	if !r.InheritPermissions {
		out.Grants = effective
	}
	return out, nil
}
func (s *Service) effectivePermissions(ctx context.Context, r *ent.Resource) (string, []Permission, error) {
	for depth := 0; r.InheritPermissions; depth++ {
		if r.ParentID == nil || depth >= MaxDepth {
			return "", nil, invalid("invalid permission ancestry")
		}
		parent, e := s.Client.Resource.Get(ctx, *r.ParentID)
		if e != nil {
			return "", nil, e
		}
		r = parent
	}
	if r.Kind == resource.KindWorkspace {
		return r.ID, []Permission{{"workspace_visitor", r.ID, "read", "allow"}, {"workspace_member", r.ID, "write", "allow"}, {"workspace_owner", r.ID, "manage", "allow"}}, nil
	}
	rows, e := s.Client.Grant.Query().Where(grant.ResourceIDEQ(r.ID)).Order(ent.Asc(grant.FieldPrincipalType, grant.FieldPrincipalID, grant.FieldAction)).All(ctx)
	if e != nil {
		return "", nil, e
	}
	permissions := []Permission{}
	for _, g := range rows {
		permissions = append(permissions, Permission{string(g.PrincipalType), g.PrincipalID, string(g.Action), string(g.Effect)})
	}
	return r.ID, permissions, nil
}

func checkVersion(version int) error {
	if version < 1 {
		return invalid(fmt.Sprintf("version must be positive: %d", version))
	}
	return nil
}
