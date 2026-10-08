package dms

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/grant"
	"papergo/ent/resource"
	"papergo/internal/model"
	"strings"
	"time"
)

type Service struct {
	Client      *ent.Client
	writeMu     chan struct{}
	transaction bool
}

func NewService(client *ent.Client) *Service {
	return &Service{Client: client, writeMu: make(chan struct{}, 1)}
}

// Mutations include authorization, version checks and audit records in one
// transaction. SQLite WAL allows concurrent readers; one writer per process
// avoids deferred-transaction upgrades racing with another application writer.
// Waiting for the writer slot honors ctx, so a timed-out request leaves the queue.
func (s *Service) write(ctx context.Context, fn func(*Service) error) error {
	select {
	case s.writeMu <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.writeMu }()
	tx, err := s.Client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	err = fn(&Service{Client: tx.Client(), writeMu: s.writeMu, transaction: true})
	if err != nil {
		_ = tx.Rollback()
	} else {
		err = tx.Commit()
	}
	if ent.IsConstraintError(err) {
		return ErrConflict
	}
	return err
}

// A consistent snapshot binds authorization, surface selection and hydration.
func read[T any](ctx context.Context, s *Service, fn func(*Service) (T, error)) (out T, err error) {
	if s.transaction {
		return fn(s)
	}
	tx, err := s.Client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = fn(&Service{Client: tx.Client(), writeMu: s.writeMu, transaction: true})
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
	Kind              string         `json:"kind"`
	Name              string         `json:"name"`
	Tags              []string       `json:"tags"`
	Values            map[string]any `json:"values"`
	PublishingEnabled bool           `json:"publishing_enabled"`
}

func (s *Service) Create(ctx context.Context, subject, parentID string, in CreateResource) (out *ent.Resource, err error) {
	if err = validateName(in.Name); err != nil {
		return
	}
	if err = validateTags(in.Tags); err != nil {
		return
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
	err = s.write(ctx, func(t *Service) error {
		id := uuid.NewString()
		workspaceID := id
		var parent *ent.Resource
		var containerID *string
		if parentID != "" {
			var e error
			parent, e = t.authorize(ctx, subject, parentID, "write")
			if e != nil {
				return e
			}
			workspaceID = parent.WorkspaceID
			valid := false
			switch string(parent.Kind) {
			case "workspace":
				valid = in.Kind == "list" || in.Kind == "library"
			case "list", "library":
				valid = in.Kind == "folder" || in.Kind == "item"
				v := parent.ID
				containerID = &v
			case "folder":
				valid = in.Kind == "folder" || in.Kind == "item"
				containerID = parent.ContainerID
			}
			if !valid {
				return invalid("invalid parent for resource kind")
			}
			for r, depth := parent, 1; r != nil; depth++ {
				if depth >= MaxDepth {
					return invalid("maximum hierarchy depth reached")
				}
				if r.ParentID == nil {
					break
				}
				r, e = t.Client.Resource.Get(ctx, *r.ParentID)
				if e != nil {
					return e
				}
			}
		}
		if in.Kind == "item" {
			if containerID == nil {
				return invalid("item must belong to a list or library")
			}
			defs, e := t.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(*containerID)).All(ctx)
			if e != nil {
				return e
			}
			if e = normalizeValues(defs, in.Values); e != nil {
				return e
			}
		} else if len(in.Values) > 0 {
			return invalid("custom values are supported on items only")
		}
		builder := t.Client.Resource.Create().SetID(id).SetWorkspaceID(workspaceID).SetKind(resource.Kind(in.Kind)).SetName(in.Name).SetTags(in.Tags).SetValues(in.Values).SetNillableContainerID(containerID).SetPublishingEnabled(in.PublishingEnabled)
		if parent != nil {
			builder.SetParentID(parent.ID)
		} else {
			builder.SetInheritPermissions(false)
		}
		var e error
		out, e = builder.Save(ctx)
		if e != nil {
			return e
		}
		if parent == nil {
			_, e = t.Client.Grant.Create().SetResourceID(out.ID).SetSubject(subject).SetAction(grant.ActionManage).SetEffect(grant.EffectAllow).Save(ctx)
			if e != nil {
				return e
			}
		}
		if out.Kind == resource.KindList || out.Kind == resource.KindLibrary {
			if e = t.recordSchema(ctx, subject, out); e != nil {
				return e
			}
		}
		if out.Kind == resource.KindItem {
			if _, e = t.recordRevision(ctx, subject, out, nil); e != nil {
				return e
			}
		}
		out, e = t.Client.Resource.Get(ctx, out.ID)
		if e != nil {
			return e
		}
		if out.Kind == resource.KindItem {
			if e = t.overlayHead(ctx, out); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "resource.create", out, map[string]any{"kind": in.Kind})
	})
	return
}

type UpdateResource struct {
	Name              *string         `json:"name,omitempty"`
	Tags              *[]string       `json:"tags,omitempty"`
	Values            *map[string]any `json:"values,omitempty"`
	PublishingEnabled *bool           `json:"publishing_enabled,omitempty"`
}

func (s *Service) Update(ctx context.Context, subject, id string, version int, in UpdateResource) (out *ent.Resource, err error) {
	if in.Name == nil && in.Tags == nil && in.Values == nil && in.PublishingEnabled == nil {
		return nil, invalid("at least one change is required")
	}
	if in.Name != nil {
		if err = validateName(*in.Name); err != nil {
			return
		}
	}
	if in.Tags != nil {
		if err = validateTags(*in.Tags); err != nil {
			return
		}
	}
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "write")
		if e != nil {
			return e
		}
		if r.Version != version {
			return ErrConflict
		}
		if in.PublishingEnabled != nil {
			if r.Kind != resource.KindList && r.Kind != resource.KindLibrary {
				return invalid("publishing_enabled belongs to lists and libraries")
			}
			if _, e = t.authorize(ctx, subject, id, "manage"); e != nil {
				return e
			}
		}
		if r.Kind == resource.KindItem {
			if e = t.overlayHead(ctx, r); e != nil {
				return e
			}
		}
		if in.Values != nil && (r.Kind != resource.KindItem || r.ContainerID == nil) {
			return invalid("only items support custom values")
		}
		// Item values are validated and normalized once, by recordRevision below.
		if r.Kind == resource.KindItem {
			if in.Name != nil {
				r.Name = *in.Name
			}
			if in.Tags != nil {
				r.Tags = *in.Tags
			}
			if in.Values != nil {
				r.Values = *in.Values
			}
		}
		b := t.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).AddVersion(1).SetUpdatedAt(time.Now().UTC())
		if in.Name != nil {
			b.SetName(*in.Name)
		}
		if in.Tags != nil {
			b.SetTags(*in.Tags)
		}
		if r.Kind == resource.KindItem {
			b.SetValues(r.Values)
		}
		if in.PublishingEnabled != nil {
			b.SetPublishingEnabled(*in.PublishingEnabled)
		}
		n, e := b.Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		out, e = t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if out.Kind == resource.KindItem {
			out.Values = r.Values
			if _, e = t.recordRevision(ctx, subject, out, nil); e != nil {
				return e
			}
			out, e = t.Client.Resource.Get(ctx, id)
			if e != nil {
				return e
			}
			if e = t.overlayHead(ctx, out); e != nil {
				return e
			}
		}
		if in.PublishingEnabled != nil && !*in.PublishingEnabled {
			if e = t.publishAllHeads(ctx, subject, out); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "resource.update", out, map[string]any{"version": out.Version})
	})
	return
}

type CreateField struct {
	Options  model.FieldOptions `json:"options,omitempty"`
	Key      string             `json:"key"`
	Label    string             `json:"label"`
	Type     string             `json:"type"`
	Required bool               `json:"required"`
	Choices  []string           `json:"choices"`
	Indexed  bool               `json:"indexed"`
	Scale    int                `json:"scale"`
}

func (s *Service) CreateField(ctx context.Context, subject, containerID string, in CreateField) (out *ent.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		out, e = t.createField(ctx, subject, c, in)
		if e != nil {
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
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
	d := fieldFromInput(in)
	if d.Choices == nil {
		d.Choices = []string{}
	}
	if e := validateFieldDefinition(d); e != nil {
		return nil, e
	}
	if e := s.validateReferenceScopes(ctx, subject, c.WorkspaceID, d); e != nil {
		return nil, e
	}
	if len(d.Options.DefaultValue) > 0 {
		if e := s.normalizeItemValues(ctx, subject, c.WorkspaceID, []*ent.FieldDefinition{d}, map[string]any{}, nil); e != nil {
			return nil, e
		}
	}
	n, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(c.ID)).Count(ctx)
	if e != nil {
		return nil, e
	}
	if n >= 200 {
		return nil, invalid("at most 200 fields per collection")
	}
	if d.Required && len(d.Options.DefaultValue) == 0 {
		exists, e := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.KindEQ(resource.KindItem)).Exist(ctx)
		if e != nil {
			return nil, e
		}
		if exists {
			return nil, invalid("required fields on populated collections need a default")
		}
	}
	return s.Client.FieldDefinition.Create().SetContainerID(c.ID).SetKey(d.Key).SetLabel(d.Label).SetType(d.Type).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetScale(d.Scale).SetOptions(d.Options).Save(ctx)
}
func (s *Service) Fields(ctx context.Context, subject, id string) ([]*ent.FieldDefinition, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.FieldDefinition, error) { return t.Fields(ctx, subject, id) })
	}
	if _, err := s.authorize(ctx, subject, id, "read"); err != nil {
		return nil, err
	}
	return s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(id)).Order(ent.Asc(fielddefinition.FieldKey)).All(ctx)
}

type Permission struct {
	Subject string `json:"subject"`
	Action  string `json:"action"`
	Effect  string `json:"effect"`
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
		if strings.TrimSpace(g.Subject) == "" || len(g.Subject) > 255 {
			return nil, invalid("invalid permission subject")
		}
		if g.Action != "read" && g.Action != "read_draft" && g.Action != "write" && g.Action != "publish" && g.Action != "manage" {
			return nil, invalid("invalid permission action")
		}
		if g.Effect != "" && g.Effect != "allow" {
			return nil, invalid("only additive allow grants are supported")
		}
		key := g.Subject + "\x00" + g.Action
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
			return ErrConflict
		}
		if r.ParentID == nil && in.Inherit {
			return invalid("workspace cannot inherit permissions")
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
				merged[g.Subject+"\x00"+g.Action] = true
			}
			for _, g := range copied {
				key := g.Subject + "\x00" + g.Action
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
			return ErrConflict
		}
		for _, g := range in.Grants {
			if _, e = t.Client.Grant.Create().SetResourceID(id).SetSubject(g.Subject).SetAction(grant.Action(g.Action)).SetEffect(grant.EffectAllow).Save(ctx); e != nil {
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
		if out.Kind == resource.KindItem {
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
	rows, e := s.Client.Grant.Query().Where(grant.ResourceIDEQ(r.ID)).Order(ent.Asc(grant.FieldSubject, grant.FieldAction)).All(ctx)
	if e != nil {
		return "", nil, e
	}
	permissions := []Permission{}
	for _, g := range rows {
		permissions = append(permissions, Permission{g.Subject, string(g.Action), string(g.Effect)})
	}
	return r.ID, permissions, nil
}

func checkVersion(version int) error {
	if version < 1 {
		return invalid(fmt.Sprintf("version must be positive: %d", version))
	}
	return nil
}
