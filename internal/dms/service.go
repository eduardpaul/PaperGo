package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/access"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/workflows"
	"papergo/internal/identity"
	"papergo/internal/model"
	"papergo/internal/requestmeta"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	DAL         *dal.Store
	db          *sql.DB
	writer      *writerSlot
	transaction bool
	// Set only while provisioning the caller's fixed Home resources.
	homeProvisioning bool
	// accepted records that a write accepted operations, for the hook
	// that wakes workers after it commits.
	accepted            *bool
	onCommit            func()
	tx                  *sql.Tx
	pending             *[]Event
	collectionTemplates map[string]CollectionTemplate
	auditProperties     map[string]map[string]bool
	traceID             string
	ops                 *operationRuntime
	// finished collects the operations a write ended, for the hook that
	// runs after it commits.
	finished               *[]string
	integrations           *integrationRuntime
	integrationWrite       bool
	pendingIntegrationData *IntegrationData
	// batch is set while one request applies many operations in a transaction.
	batch *batchCache
	// ext holds what extensions plug into the service.
	ext *extensions
}

// NewService serves the PaperGo database db.
func NewService(db *sql.DB) *Service {
	return &Service{DAL: dal.NewStore(db), db: db, writer: newWriterSlot(), collectionTemplates: builtInCollectionTemplates(), ops: &operationRuntime{handlers: builtInOperationHandlers(), holder: "operations/" + uuid.NewString()}, ext: &extensions{}}
}

// OnCommit calls f after each committed write that recorded domain events,
// so a dispatcher can pick them up at once. Call it before the service is used.
func (s *Service) OnCommit(f func()) { s.onCommit = f }

// Mutations include authorization, version checks, audit records and the
// domain event log in one transaction. SQLite WAL allows concurrent readers; one writer
// per process keeps PaperGo writes in order and takes the slot before a
// connection, so waiting writers never hold the connections readers need.
// Background writes (workflow steps) yield the slot to other writes. Waiting
// for the slot honors ctx, so a timed-out request leaves the queue.
func (s *Service) write(ctx context.Context, fn func(*Service) error) error {
	if s.transaction {
		return fn(s)
	}
	var recorded int
	var finished []string
	var accepted bool
	err := func() error {
		if err := s.writer.acquire(ctx); err != nil {
			return err
		}
		defer s.writer.release()
		return dal.Retry(ctx, func() error {
			finished, accepted = finished[:0], false
			tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				return err
			}
			if recorded, err = s.inTx(ctx, tx, &finished, &accepted, fn); err != nil {
				_ = tx.Rollback()
				return err
			}
			return tx.Commit()
		})
	}()
	if dberr.IsConstraintError(err) {
		return ErrConflict
	}
	// Hooks run after the writer is free, so they may write themselves.
	if err == nil && recorded > 0 && s.onCommit != nil {
		s.onCommit()
	}
	if err == nil && len(finished) > 0 {
		s.ops.ended(finished)
	}
	if err == nil && accepted {
		s.ops.wake()
	}
	return err
}

// inTx runs fn on a service bound to tx and then records the events fn
// raised in the domain event log, so they commit or roll back with its
// writes. It returns how many events it recorded.
func (s *Service) inTx(ctx context.Context, tx *sql.Tx, finished *[]string, accepted *bool, fn func(*Service) error) (int, error) {
	ctx = requestmeta.EnsureTrace(ctx)
	var pending []Event
	t := &Service{DAL: s.DAL.WithTx(tx), db: s.db, writer: s.writer, transaction: true, tx: tx, pending: &pending, finished: finished, accepted: accepted, collectionTemplates: s.collectionTemplates, ops: s.ops, ext: s.ext}
	t.integrations = s.integrations
	t.traceID = requestmeta.Trace(ctx)
	t.auditProperties = map[string]map[string]bool{}
	if err := fn(t); err != nil {
		return 0, err
	}
	if err := recordEvents(ctx, t.DAL, pending); err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, event := range pending {
		if event.ResourceID != "" && !seen[event.ResourceID] {
			if err := t.collectRevisions(ctx, event.ResourceID); err != nil {
				return 0, err
			}
			seen[event.ResourceID] = true
		}
	}
	return len(pending), nil
}

// A consistent snapshot binds authorization, surface selection and hydration.
func read[T any](ctx context.Context, s *Service, fn func(*Service) (T, error)) (out T, err error) {
	if s.transaction {
		return fn(s)
	}
	err = dal.Retry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if out, err = fn(&Service{DAL: s.DAL.WithTx(tx), db: s.db, writer: s.writer, transaction: true, tx: tx, collectionTemplates: s.collectionTemplates, ops: s.ops, integrations: s.integrations, ext: s.ext}); err != nil {
			return err
		}
		return tx.Commit()
	})
	return out, err
}
func (s *Service) audit(ctx context.Context, subject, action string, r *resources.Resource, details map[string]any) error {
	return s.RecordEntity(ctx, subject, action, AuditTarget{WorkspaceID: r.WorkspaceID, ResourceID: r.ID, Kind: string(r.Kind)}, details)
}
func (s *Service) Get(ctx context.Context, subject, id string) (*resources.Resource, error) {
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
	Keywords          []string       `json:"keywords"`
	Values            map[string]any `json:"values"`
	Versioning        *string        `json:"versioning,omitempty"`
	MaxVersions       *int           `json:"max_versions,omitempty"`
	PublishingEnabled bool           `json:"publishing_enabled"`
	WebDAVEnabled     bool           `json:"webdav_enabled"`
}

func (s *Service) Create(ctx context.Context, subject, parentID string, in CreateResource) (out *resources.Resource, err error) {
	out, _, err = s.CreateWithStatus(ctx, subject, parentID, in)
	return
}

// create inserts a resource inside a write transaction. A library item may be
// created with its first blob, so its first revision already carries content.
func (s *Service) create(ctx context.Context, subject, parentID string, in CreateResource, content *BlobInput) (*resources.Resource, error) {
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
	if in.Versioning != nil {
		versioning = *in.Versioning
	}
	scope := "item.write"
	if in.Kind == "workspace" {
		scope = "workspace.create"
	} else if collection {
		scope = "item.read"
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
	if err := validateResourceName(in.Kind, in.Name); err != nil {
		return nil, err
	}
	if in.Kind == "item" || in.Kind == "folder" {
		in.Name = strings.TrimSpace(in.Name)
	}
	if err := validateKeywords(in.Keywords); err != nil {
		return nil, err
	}
	var keywordErr error
	if in.Keywords, keywordErr = s.normalizeKeywords(ctx, subject, in.Keywords, nil); keywordErr != nil {
		return nil, keywordErr
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
	var parent *resources.Resource
	var containerID *string
	if parentID != "" {
		var e error
		parent, e = s.authorizeWrite(ctx, subject, parentID)
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
		if in.Kind == "folder" && depth >= MaxFolderDepth+2 {
			return nil, invalid("maximum folder depth reached")
		}
	}
	if in.Kind == "folder" && containerID != nil {
		c, err := s.DAL.Resources().Resource(ctx, *containerID)
		if err != nil {
			return nil, err
		}
		if !c.AllowFolders {
			return nil, invalid("this collection does not allow new folders")
		}
	}
	key, e := s.fileNameKey(ctx, refs.ResourceKind(in.Kind), containerID, in.Name)
	if e != nil {
		return nil, e
	}
	var typeID *string
	if in.Kind == "item" || in.Kind == "folder" {
		if containerID == nil {
			return nil, invalid("item must belong to a list or library")
		}
		defs, e := s.collectionDefinitions(ctx, *containerID)
		if e != nil {
			return nil, e
		}
		typ, e := s.contentType(ctx, *containerID, in.ContentTypeID)
		if e != nil {
			return nil, e
		}
		typeID = &typ.ID
		selected, _ := resourceDefinitions(refs.ResourceKind(in.Kind), selectTypeDefinitions(defs, typ), nil)
		if e = normalizeRevisionValues(selected, in.Values, nil, in.Kind == "item"); e != nil {
			return nil, e
		}
	} else if len(in.Values) > 0 {
		return nil, invalid("custom values are supported on items and folders only")
	}
	rawValues, e := json.Marshal(in.Values)
	if e != nil {
		return nil, e
	}
	input := resources.New{ID: id, WorkspaceID: workspaceID, Kind: refs.ResourceKind(in.Kind), Name: in.Name, Description: in.Description, AllowFolders: in.AllowFolders, Keywords: in.Keywords, Values: rawValues, PublishingEnabled: in.PublishingEnabled, WebdavEnabled: in.WebDAVEnabled, Versioning: resources.Versioning(versioning), CreatedBy: subject}
	if containerID != nil {
		input.ContainerID = *containerID
	}
	if key != nil {
		input.NameKey = *key
	}
	if typeID != nil {
		input.ContentTypeID = *typeID
	}
	if parent != nil {
		input.ParentID = parent.ID
	}
	if in.MaxVersions != nil {
		input.MaxVersions = *in.MaxVersions
	}
	if in.DuplicatePolicy != nil {
		input.DuplicatePolicy = resources.DuplicatePolicy(*in.DuplicatePolicy)
	}
	if template != nil {
		if e = s.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		in.ContentTypeIDs, e = s.provisionCollectionTypes(ctx, subject, *template)
		if e != nil {
			return nil, e
		}
		input.TemplateKeys = []string{template.Key}
	}
	if s.homeProvisioning {
		if in.Kind == "workspace" {
			input.PersonalOwnerID = subject
		}
		if collection {
			input.SystemKey = resources.SystemKey("home." + strings.ToLower(in.Name))
		}
	}
	created, e := s.DAL.Resources().Create(ctx, input)
	if e != nil {
		return nil, e
	}
	if parent == nil {
		if _, e = s.DAL.Grants().Create(ctx, grants.Grant{ResourceID: created.ID, PrincipalType: grants.PrincipalUser, PrincipalID: subject, Action: grants.ActionManage}); e != nil {
			return nil, e
		}
	}
	// Create returns the stored row; preserve exact input numbers until
	// revision validation.
	out := created
	out.Values = in.Values

	if out.Kind == refs.KindList || out.Kind == refs.KindLibrary {
		ids := in.ContentTypeIDs
		if len(ids) == 0 {
			typ, e := s.persistContentType(ctx, ContentTypeInput{Key: "item", Name: "Item"}, "")
			if e != nil {
				return nil, e
			}
			ids = []string{typ.ID}
		} else if e = s.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		for i, id := range ids {
			if _, e = s.DAL.Catalog().ContentType(ctx, id); dberr.IsNotFound(e) {
				return nil, ErrNotFound
			} else if e != nil {
				return nil, e
			}
			if _, e = s.DAL.Catalog().Attach(ctx, out.ID, id, i == 0); e != nil {
				return nil, e
			}
		}
		if e = s.refreshCollections(ctx, subject, []*resources.Resource{out}); e != nil {
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
	if out.Collection() {
		if e = s.provisionArchivedView(ctx, out.ID); e != nil {
			return nil, e
		}
	}
	details := map[string]any{"kind": in.Kind}
	if contentKind(out.Kind) {
		var file *blobs.Blob
		var blobID *string
		if content != nil {
			if file, e = s.createBlob(ctx, subject, out.ID, *content, true); e != nil {
				return nil, e
			}
			blobID = &file.ID
			details["blob_id"] = file.ID
		}
		rev, e := s.recordRevision(ctx, subject, out, blobID, false)
		if e != nil {
			return nil, e
		}
		if file != nil {
			s.emitFileChange(ctx, subject, out, file, rev, content.Source, nil)
		}
	}
	out, e = s.DAL.Resources().Resource(ctx, out.ID)
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
func (s *Service) depth(ctx context.Context, r *resources.Resource) (int, error) {
	depth := 1
	for ; r.ParentID != nil; depth++ {
		if depth > MaxResourceDepth {
			return depth, invalid("invalid hierarchy depth")
		}
		// Lists and libraries always sit directly in a workspace.
		if r.Kind == refs.KindList || r.Kind == refs.KindLibrary {
			return depth + 1, nil
		}
		var err error
		if r, err = s.DAL.Resources().Resource(ctx, *r.ParentID); err != nil {
			return depth, err
		}
	}
	return depth, nil
}

type UpdateResource struct {
	DefaultCurrency   *string         `json:"default_currency,omitempty"`
	DuplicatePolicy   *string         `json:"duplicate_policy,omitempty"`
	Name              *string         `json:"name,omitempty"`
	Description       *string         `json:"description,omitempty"`
	AllowFolders      *bool           `json:"allow_folders,omitempty"`
	Keywords          *[]string       `json:"keywords,omitempty"`
	Values            *map[string]any `json:"values,omitempty"`
	ContentTypeID     *string         `json:"content_type_id,omitempty"`
	Versioning        *string         `json:"versioning,omitempty"`
	MaxVersions       *int            `json:"max_versions,omitempty"`
	PublishingEnabled *bool           `json:"publishing_enabled,omitempty"`
	WebDAVEnabled     *bool           `json:"webdav_enabled,omitempty"`
	// ParentID moves a folder or item to another folder of the same collection.
	ParentID *string `json:"parent_id,omitempty"`
}

func (s *Service) Update(ctx context.Context, subject, id string, version int, in UpdateResource) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		if out, e = t.update(ctx, subject, id, version, in); e != nil {
			return e
		}
		return t.attachHeadFile(ctx, out)
	})
	return
}

// attachHeadFile sets the File of an item's head revision, the one its
// writer sees after a change.
func (s *Service) attachHeadFile(ctx context.Context, r *resources.Resource) error {
	if r.Kind != refs.KindItem || r.HeadRevisionID == nil {
		return nil
	}
	rev, err := s.revision(ctx, *r.HeadRevisionID)
	if err != nil {
		return err
	}
	_, err = s.attachFiles(ctx, []*resources.Resource{r}, map[string]*revisions.Revision{r.ID: rev})
	return err
}
func (s *Service) update(ctx context.Context, subject, id string, version int, in UpdateResource) (*resources.Resource, error) {
	content := in.Name != nil || in.Description != nil || in.Keywords != nil || in.Values != nil || in.ContentTypeID != nil
	if !content && in.DefaultCurrency == nil && in.DuplicatePolicy == nil && in.PublishingEnabled == nil && in.WebDAVEnabled == nil && in.AllowFolders == nil && in.ParentID == nil && in.Versioning == nil && in.MaxVersions == nil {
		return nil, invalid("at least one change is required")
	}
	if in.Keywords != nil {
		if err := validateKeywords(*in.Keywords); err != nil {
			return nil, err
		}
	}
	r, e := s.authorize(ctx, subject, id, "write")
	if e != nil {
		return nil, e
	}
	if in.Keywords == nil && content && contentKind(r.Kind) {
		keywords := append([]string{}, r.Keywords...)
		in.Keywords = &keywords
	}
	if in.Keywords != nil {
		normalized, err := s.normalizeKeywords(ctx, subject, *in.Keywords, r.Keywords)
		if err != nil {
			return nil, err
		}
		in.Keywords = &normalized
	}
	if in.Name != nil {
		if err := validateResourceName(string(r.Kind), *in.Name); err != nil {
			return nil, err
		}
		if contentKind(r.Kind) {
			name := strings.TrimSpace(*in.Name)
			in.Name = &name
		}
	}
	if err := validateDuplicatePolicy(string(r.Kind), in.DuplicatePolicy); err != nil {
		return nil, err
	}
	collection := r.Kind == refs.KindList || r.Kind == refs.KindLibrary
	if err := validateHistorySettings(collection, in.Versioning, in.MaxVersions); err != nil {
		return nil, err
	}
	if r.Kind == refs.KindWorkspace || collection {
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
	if in.DefaultCurrency != nil && (r.Kind != refs.KindWorkspace || (*in.DefaultCurrency != "" && !model.ValidCurrencyCode(*in.DefaultCurrency))) {
		return nil, invalid("default_currency requires a workspace and an uppercase three-letter currency code, or empty to inherit")
	}
	if in.AllowFolders != nil && !collection {
		return nil, invalid("allow_folders belongs to lists and libraries")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	if in.PublishingEnabled != nil || in.WebDAVEnabled != nil {
		if in.PublishingEnabled != nil && r.Kind != refs.KindList && r.Kind != refs.KindLibrary {
			return nil, invalid("publishing_enabled belongs to lists and libraries")
		}
		if in.WebDAVEnabled != nil && r.Kind != refs.KindLibrary {
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
	beforeAudit, e := json.Marshal(r)
	if e != nil {
		return nil, e
	}
	if e = s.protectIntegrationFields(ctx, r, in); e != nil {
		return nil, e
	}
	if in.Values != nil && (!contentKind(r.Kind) || r.ContainerID == nil) {
		return nil, invalid("only items and folders support custom values")
	}
	if in.ContentTypeID != nil && (!contentKind(r.Kind) || *in.ContentTypeID == "") {
		return nil, invalid("content_type_id requires an attached content type")
	}
	moved := in.ParentID != nil && (r.ParentID == nil || *in.ParentID != *r.ParentID)
	changes := resources.Changes{Name: in.Name, Description: in.Description, ParentID: in.ParentID, ContentTypeID: in.ContentTypeID, AllowFolders: in.AllowFolders, DefaultCurrency: in.DefaultCurrency, MaxVersions: in.MaxVersions, PublishingEnabled: in.PublishingEnabled, WebdavEnabled: in.WebDAVEnabled, Keywords: in.Keywords}
	if in.Versioning != nil {
		versioning := resources.Versioning(*in.Versioning)
		changes.Versioning = &versioning
	}
	if in.DuplicatePolicy != nil {
		policy := resources.DuplicatePolicy(*in.DuplicatePolicy)
		changes.DuplicatePolicy = &policy
	}
	if moved {
		if e = s.checkMove(ctx, subject, r, *in.ParentID); e != nil {
			return nil, e
		}
	}
	if in.Name != nil {
		key, e := s.fileNameKey(ctx, r.Kind, r.ContainerID, *in.Name)
		if e != nil {
			return nil, e
		}
		changes.NameKey = key
	}
	// Item values are validated and normalized once, by recordRevision below.
	if contentKind(r.Kind) {
		if in.ContentTypeID != nil && *in.ContentTypeID != *r.ContentTypeID {
			c, err := s.DAL.Resources().Resource(ctx, *r.ContainerID)
			if err != nil {
				return nil, err
			}
			schema, err := s.DAL.Catalog().Schema(ctx, *c.SchemaHeadID)
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
		}
		if in.Name != nil {
			r.Name = *in.Name
		}
		if in.Keywords != nil {
			r.Keywords = *in.Keywords
		}
		if in.Values != nil {
			// Prune fields removed from the live schema before applying this patch.
			c, err := s.DAL.Resources().Resource(ctx, *r.ContainerID)
			if err != nil {
				return nil, err
			}
			schema, err := s.DAL.Catalog().Schema(ctx, *c.SchemaHeadID)
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
				if d := known[key]; (d.Type == catalog.FieldLookup || d.Type == catalog.FieldPerson) && (*in.Values)[key] != nil {
					assigned := map[string]any{key: (*in.Values)[key]}
					if err = s.normalizeItemValues(ctx, subject, []*catalog.FieldDefinition{d}, assigned, nil, true); err != nil {
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
		raw, err := json.Marshal(r.Values)
		if err != nil {
			return nil, err
		}
		changes.Values = raw
	}
	if e = s.DAL.Resources().Change(ctx, id, version, subject, changes); e != nil {
		if errors.Is(e, dberr.ErrConcurrentUpdate) {
			return nil, ErrPreconditionFailed
		}
		return nil, e
	}
	out, e := s.DAL.Resources().Resource(ctx, id)
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
			if out, e = s.DAL.Resources().Resource(ctx, id); e != nil {
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
		// Heads are published now; the collection keeps one surface per item.
		if e = s.dropHeadSurfaces(ctx, out.ID); e != nil {
			return nil, e
		}
	}
	if in.PublishingEnabled != nil && *in.PublishingEnabled && !r.PublishingEnabled {
		if e = s.addHeadSurfaces(ctx, out); e != nil {
			return nil, e
		}
	}
	details := map[string]any{"version": out.Version}
	if moved {
		details["from_parent_id"] = *r.ParentID
		details["parent_id"] = *in.ParentID
		// A folder's move without a content change records no revision, so
		// it raises its own event.
		if out.Kind == refs.KindFolder && !content {
			s.emit(ctx, EventFolderUpdated, subject, out.WorkspaceID, out.ContainerID, out.ID, map[string]any{"from_parent_id": *r.ParentID, "parent_id": *in.ParentID})
		}
		s.emitScopeMoved(ctx, subject, r, out)
	}
	if e = s.trackAuditProperties(id, beforeAudit, out, resourceAuditProperties...); e != nil {
		return nil, e
	}
	return out, s.audit(ctx, subject, "resource.update", out, details)
}

// checkMove validates moving r below parentID: both ends need write access,
// the parent must belong to r's collection, and the subtree must stay within
// MaxFolderDepth without entering itself. DAL enforces containment during the write.
func (s *Service) checkMove(ctx context.Context, subject string, r *resources.Resource, parentID string) error {
	if r.Kind != refs.KindFolder && r.Kind != refs.KindItem {
		return invalid("only folders and items can move")
	}
	p, err := s.authorize(ctx, subject, parentID, "write")
	if err != nil {
		return err
	}
	if r.ContainerID == nil || (p.ID != *r.ContainerID && (p.Kind != refs.KindFolder || p.ContainerID == nil || *p.ContainerID != *r.ContainerID)) {
		return invalid("folders and items move only within their collection")
	}
	// One walk up from the new parent both rejects entering r and counts depth.
	depth := 1
	for a := p; a.ParentID != nil; depth++ {
		if a.ID == r.ID {
			return invalid("a folder cannot move into itself")
		}
		if depth > MaxResourceDepth {
			return invalid("invalid hierarchy depth")
		}
		if a, err = s.DAL.Resources().Resource(ctx, *a.ParentID); err != nil {
			return err
		}
	}
	height, err := s.subtreeFolderHeight(ctx, r.ID)
	if err != nil {
		return err
	}
	if depth-2+height > MaxFolderDepth {
		return invalid("maximum folder depth reached")
	}
	return nil
}

// subtreeFolderHeight counts folder levels, excluding a terminal item.
func (s *Service) subtreeFolderHeight(ctx context.Context, id string) (int, error) {
	return s.DAL.Resources().FolderHeight(ctx, id, MaxFolderDepth+1)
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
	if r.PersonalOwnerID != nil || r.SystemKey != nil {
		return ErrProtectedHome
	}
	collection := r.Kind == refs.KindList || r.Kind == refs.KindLibrary
	if r.Kind == refs.KindWorkspace || collection {
		if _, err = s.authorize(ctx, subject, id, "manage"); err != nil {
			return err
		}
	}
	if r.Version != version {
		return ErrPreconditionFailed
	}
	if r.Kind == refs.KindFolder && !recursive {
		populated, err := s.DAL.Resources().Exists(ctx, resources.Filter{ParentID: id, State: resources.Live})
		if err != nil {
			return err
		}
		if populated {
			return ErrFolderNotEmpty
		}
	}
	subtree, err := s.DAL.Resources().SubtreeIDs(ctx, id, resources.TreeFilter{State: resources.Live})
	if err != nil {
		return err
	}
	writable, err := s.DAL.Resources().Count(ctx, resources.Filter{IDs: subtree, State: resources.Live, Reader: &resources.Reader{Principal: principal(ctx, subject), Action: access.Write}})
	if err != nil {
		return err
	}
	total := len(subtree)
	// Explicit recursion authorizes every descendant before removing any content.
	if !collection && writable != total {
		return ErrForbidden
	}
	links, err := s.DAL.Relationships().Count(ctx, relationships.Filter{Endpoints: subtree})
	if err != nil {
		return err
	}
	// Schedules and event dispatch only select enabled, live workflows.
	var retired []workflows.Workflow
	switch {
	case r.Kind == refs.KindWorkspace:
		retired, err = s.DAL.Workflows().Retire(ctx, id, "", subject)
	case collection:
		retired, err = s.DAL.Workflows().Retire(ctx, "", id, subject)
	}
	if err != nil {
		return err
	}
	for _, w := range retired {
		s.emit(ctx, EventWorkflowDeleted, subject, w.WorkspaceID, nil, "", map[string]any{"workflow_id": w.ID})
	}
	deleted, err := s.DAL.Resources().Delete(ctx, id, subject)
	if err != nil {
		return err
	}
	if s.pending != nil {
		for _, entry := range deleted {
			switch entry.Kind {
			case refs.KindItem:
				s.emit(ctx, EventItemDeleted, subject, entry.WorkspaceID, entry.ContainerID, entry.ID, nil)
			case refs.KindFolder:
				s.emit(ctx, EventFolderDeleted, subject, entry.WorkspaceID, entry.ContainerID, entry.ID, nil)
			}
		}
	}
	return s.audit(ctx, subject, "resource.delete", r, map[string]any{"version": version + 1, "descendants": total - 1, "relationships": links})
}

// fileNameKey is the unique, case-insensitive name of a library folder or item,
// and nil for other resources. Library names must be usable as file names.
func (s *Service) fileNameKey(ctx context.Context, kind refs.ResourceKind, containerID *string, name string) (*string, error) {
	if (kind != refs.KindFolder && kind != refs.KindItem) || containerID == nil {
		return nil, nil
	}
	c, err := s.batchCollection(ctx, *containerID)
	if err != nil {
		return nil, err
	}
	if c.Kind != refs.KindLibrary {
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
	Type          catalog.FieldType  `json:"type"`
	Required      bool               `json:"required"`
	Choices       []string           `json:"choices"`
	Indexed       bool               `json:"indexed"`
	Scale         int                `json:"scale"`
}

func (s *Service) CreateField(ctx context.Context, subject, containerID string, in CreateField) (out *catalog.FieldDefinition, err error) {
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
		return t.audit(ctx, subject, "field.create", c, map[string]any{"key": in.Key, "field_id": out.ID})
	})
	return
}
func (s *Service) createField(ctx context.Context, subject string, c *resources.Resource, in CreateField) (*catalog.FieldDefinition, error) {
	if c.Kind != "list" && c.Kind != "library" {
		return nil, invalid("fields belong to collections")
	}
	typ, e := s.contentType(ctx, c.ID, in.ContentTypeID)
	if e != nil {
		return nil, e
	}
	held, e := s.schemaFieldIDs(ctx, c.ID)
	if e != nil {
		return nil, e
	}
	if _, used := held[in.Key]; used {
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
		if e := s.normalizeKeywordDefault(ctx, subject, d); e != nil {
			return nil, e
		}
	}
	n, e := s.DAL.Catalog().CountFields(ctx, catalog.FieldFilter{CollectionID: c.ID})
	if e != nil {
		return nil, e
	}
	if n >= 200 {
		return nil, invalid("at most 200 fields per collection")
	}
	if d.Required && len(d.Options.DefaultValue) == 0 {
		exists, e := s.DAL.Resources().Exists(ctx, resources.Filter{ContainerID: c.ID, ContentTypeID: typ.ID, Kinds: []refs.ResourceKind{refs.KindItem}, State: resources.Live})
		if e != nil {
			return nil, e
		}
		if exists {
			return nil, invalid("required fields on populated collections need a default")
		}
	}
	out, e := s.persistFieldDefinition(ctx, d)
	if e != nil {
		return nil, e
	}
	if _, e = s.reviseContentType(ctx, typ, typ.Name, append(append([]string{}, typ.FieldIds...), out.ID), typ.Rules); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Fields(ctx context.Context, subject, id string) ([]*catalog.FieldDefinition, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*catalog.FieldDefinition, error) { return t.Fields(ctx, subject, id) })
	}
	if _, err := s.authorize(ctx, subject, id, "read"); err != nil {
		return nil, err
	}
	return s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: id})
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

func (s *Service) SetPermissions(ctx context.Context, subject, id string, version int, in Permissions) (out *resources.Resource, err error) {
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
		if r.Kind == refs.KindWorkspace {
			return invalid("workspace access is managed through members")
		}
		for _, g := range in.Grants {
			switch g.PrincipalType {
			case "user", "group":
				if e = identity.ValidatePrincipal(ctx, dal.NewStore(t.db).WithTx(t.tx), identity.Principal{Type: directory.PrincipalType(g.PrincipalType), ID: g.PrincipalID}); e != nil {
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
		previous, e := t.Permissions(ctx, subject, id)
		if e != nil {
			return e
		}
		if e = t.DAL.Grants().Clear(ctx, id); e != nil {
			return e
		}
		if e := t.DAL.Resources().SetInheritPermissions(ctx, id, version, in.Inherit); e != nil {
			if errors.Is(e, dberr.ErrConcurrentUpdate) {
				return ErrPreconditionFailed
			}
			return e
		}
		for _, g := range in.Grants {
			if _, e = t.DAL.Grants().Create(ctx, grants.Grant{ResourceID: id, PrincipalType: grants.PrincipalType(g.PrincipalType), PrincipalID: g.PrincipalID, Action: grants.Action(g.Action)}); e != nil {
				return e
			}
		}
		out, e = t.DAL.Resources().Resource(ctx, id)
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
		if !samePermissions(previous, in) {
			current, e := t.Permissions(ctx, subject, id)
			if e != nil {
				return e
			}
			t.emitPermissionsChanged(ctx, subject, out, map[string]any{
				"action": "replace", "previous": previous, "current": current,
			})
		}
		if r.InheritPermissions != out.InheritPermissions {
			if t.auditProperties[id] == nil {
				t.auditProperties[id] = map[string]bool{}
			}
			t.auditProperties[id]["inherit_permissions"] = true
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
func (s *Service) effectivePermissions(ctx context.Context, r *resources.Resource) (string, []Permission, error) {
	for depth := 0; r.InheritPermissions; depth++ {
		if r.ParentID == nil || depth >= MaxResourceDepth {
			return "", nil, invalid("invalid permission ancestry")
		}
		parent, e := s.DAL.Resources().Resource(ctx, *r.ParentID)
		if e != nil {
			return "", nil, e
		}
		r = parent
	}
	if r.Kind == refs.KindWorkspace {
		return r.ID, []Permission{{"workspace_visitor", r.ID, "read", "allow"}, {"workspace_member", r.ID, "write", "allow"}, {"workspace_owner", r.ID, "manage", "allow"}}, nil
	}
	rows, e := s.DAL.Grants().List(ctx, grants.Filter{ResourceID: r.ID})
	if e != nil {
		return "", nil, e
	}
	permissions := []Permission{}
	for _, g := range rows {
		permissions = append(permissions, Permission{string(g.PrincipalType), g.PrincipalID, string(g.Action), "allow"})
	}
	return r.ID, permissions, nil
}
