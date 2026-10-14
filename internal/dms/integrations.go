package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/integrationitem"
	"papergo/ent/listintegration"
	"papergo/ent/resource"
	"papergo/internal/identity"
	"papergo/internal/integration"
)

const IntegrationKindICalendar = "ical_https"
const OperationKindIntegrationRefresh = "integration.refresh"
const integrationLease = 2 * time.Minute

var ErrIntegrationBusy = errors.New("integration is refreshing; try again after it finishes")

type integrationRuntime struct {
	activate func(context.Context, *Service, string, *ent.Resource) error
	secrets  *integration.Secrets
	ical     *iCalendarRuntime
}

// ConfigureIntegrations supplies the deployment key before serving requests.
func (s *Service) ConfigureIntegrations(secrets *integration.Secrets) {
	s.integrations = &integrationRuntime{secrets: secrets, ical: newICalendarRuntime()}
}

func (s *Service) OnIntegrationCreated(f func(context.Context, *Service, string, *ent.Resource) error) {
	if s.integrations != nil {
		s.integrations.activate = f
	}
}

type IntegrationInput struct {
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Configuration json.RawMessage `json:"configuration"`
}
type UpdateIntegration struct {
	Name          *string          `json:"name,omitempty"`
	Paused        *bool            `json:"paused,omitempty"`
	Configuration *json.RawMessage `json:"configuration,omitempty"`
	Reset         bool             `json:"reset,omitempty"`
}

func integrationName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return "", invalid("integration name needs 1 to 255 characters")
	}
	return name, nil
}
func (s *Service) integrationList(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	if action == "manage" {
		if err := s.RequireScope(ctx, subject, "list.write"); err != nil {
			return nil, err
		}
	}
	c, err := s.authorize(ctx, subject, id, action)
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindList {
		return nil, invalid("integrations belong to ordinary lists")
	}
	return c, nil
}
func (s *Service) integration(ctx context.Context, subject, list, id, action string) (*ent.ListIntegration, error) {
	if _, err := s.integrationList(ctx, subject, list, action); err != nil {
		return nil, err
	}
	row, err := s.Client.ListIntegration.Query().Where(listintegration.IDEQ(id), listintegration.ContainerIDEQ(list)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return row, err
}
func (s *Service) ListIntegrations(ctx context.Context, subject, list string) ([]*ent.ListIntegration, error) {
	return read(ctx, s, func(t *Service) ([]*ent.ListIntegration, error) {
		if _, err := t.integrationList(ctx, subject, list, "read"); err != nil {
			return nil, err
		}
		return t.Client.ListIntegration.Query().Where(listintegration.ContainerIDEQ(list)).Order(ent.Asc(listintegration.FieldCreatedAt), ent.Asc(listintegration.FieldID)).All(ctx)
	})
}
func (s *Service) validateIntegrationURL(ctx context.Context, subject, list, raw string) (string, error) {
	if s.integrations == nil {
		return "", invalid("list integrations are not configured")
	}
	if _, err := read(ctx, s, func(t *Service) (*ent.Resource, error) { return t.integrationList(ctx, subject, list, "manage") }); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := s.integrations.ical.validateURL(ctx, raw)
	if err != nil {
		return "", invalid(err.Error())
	}
	return u, nil
}
func integrationCredentials(ctx context.Context, subject string, b *ent.ListIntegrationCreate) *ent.ListIntegrationCreate {
	b.SetConfiguredBy(subject)
	if token, limited := identity.RESTTokenContext(ctx); limited {
		b.SetCredentialID(token.ID).SetCredentialScopes(token.Scopes)
	}
	return b
}
func (s *Service) CreateIntegration(ctx context.Context, subject, list string, in IntegrationInput) (out *ent.ListIntegration, err error) {
	if in.Kind != IntegrationKindICalendar {
		return nil, invalid("integration kind must be ical_https")
	}
	name, err := integrationName(in.Name)
	if err != nil {
		return nil, err
	}
	config, err := decodeICalendarConfig(in.Configuration)
	if err != nil {
		return nil, err
	}
	raw, err := s.validateIntegrationURL(ctx, subject, list, config.URL)
	if err != nil {
		return nil, err
	}
	cipher, err := s.integrations.secrets.Encrypt(list, string(mustIntegrationConfig(raw)))
	if err != nil {
		return nil, err
	}
	err = s.write(ctx, func(t *Service) error {
		c, e := t.integrationList(ctx, subject, list, "manage")
		if e != nil {
			return e
		}
		count, e := t.Client.ListIntegration.Query().Where(listintegration.ContainerIDEQ(list)).Count(ctx)
		if e != nil {
			return e
		}
		if count >= 50 {
			return invalid("at most 50 integrations per list")
		}
		if _, e = t.ensureEventType(ctx, subject, c); e != nil {
			return e
		}
		b := t.Client.ListIntegration.Create().SetContainerID(list).SetKind(IntegrationKindICalendar).SetName(name).SetGeneration(uuid.NewString()).SetConfigCiphertext(cipher).SetConfigHash(t.integrations.secrets.Hash(raw))
		out, e = integrationCredentials(ctx, subject, b).Save(ctx)
		if e != nil {
			return e
		}
		if _, e = t.enqueueIntegrationRefresh(ctx, subject, out); e != nil {
			return e
		}
		if t.integrations.activate != nil {
			if e = t.integrations.activate(ctx, t, subject, c); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "integration.create", c, map[string]any{"integration_id": out.ID, "kind": in.Kind})
	})
	return
}
func (s *Service) checkIntegrationLease(row *ent.ListIntegration) error {
	if row.LeaseUntil != nil && row.LeaseUntil.After(time.Now().UTC()) {
		return ErrIntegrationBusy
	}
	return nil
}
func (s *Service) UpdateIntegration(ctx context.Context, subject, list, id string, version int, in UpdateIntegration) (out *ent.ListIntegration, err error) {
	if in.Name == nil && in.Paused == nil && in.Configuration == nil {
		return nil, invalid("at least one integration change is required")
	}
	if in.Reset && in.Configuration == nil {
		return nil, invalid("reset requires replacement configuration")
	}
	var raw string
	var cipher []byte
	if in.Configuration != nil {
		config, e := decodeICalendarConfig(*in.Configuration)
		if e != nil {
			return nil, e
		}
		raw, err = s.validateIntegrationURL(ctx, subject, list, config.URL)
		if err != nil {
			return nil, err
		}
		cipher, err = s.integrations.secrets.Encrypt(list, string(mustIntegrationConfig(raw)))
		if err != nil {
			return nil, err
		}
	}
	if in.Name != nil {
		name, e := integrationName(*in.Name)
		if e != nil {
			return nil, e
		}
		in.Name = &name
	}
	err = s.write(ctx, func(t *Service) error {
		row, e := t.integration(ctx, subject, list, id, "manage")
		if e != nil {
			return e
		}
		if row.Version != version {
			return ErrPreconditionFailed
		}
		if e = t.checkIntegrationLease(row); e != nil {
			return e
		}
		b := t.Client.ListIntegration.UpdateOne(row).SetNillableName(in.Name).SetNillablePaused(in.Paused).AddVersion(1).SetConfiguredBy(subject).SetCredentialID("").SetCredentialScopes([]string{})
		if token, limited := identity.RESTTokenContext(ctx); limited {
			b.SetCredentialID(token.ID).SetCredentialScopes(token.Scopes)
		}
		if in.Configuration != nil {
			b.SetConfigCiphertext(cipher).SetConfigHash(t.integrations.secrets.Hash(raw)).SetState(json.RawMessage(`{}`)).SetError("")
			if in.Reset {
				if _, e = t.Client.IntegrationItem.Update().Where(integrationitem.IntegrationIDEQ(id)).ClearIntegrationID().Save(ctx); e != nil {
					return e
				}
				b.SetGeneration(uuid.NewString()).SetItemCount(0).ClearLastSuccessAt()
			}
		}
		out, e = b.Save(ctx)
		if e != nil {
			return e
		}
		// Supersede queued work when its configuration changes. A new operation is
		// authorized with the configuring credential, without carrying the URL.
		if e = t.supersedeIntegrationRefreshes(ctx, id); e != nil {
			return e
		}
		if !out.Paused && (in.Configuration != nil || in.Paused != nil) {
			if _, e = t.enqueueIntegrationRefresh(ctx, subject, out); e != nil {
				return e
			}
		}
		c, e := t.Client.Resource.Get(ctx, list)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "integration.update", c, map[string]any{"integration_id": id, "paused": out.Paused, "reset": in.Reset})
	})
	return
}
func (s *Service) RemoveIntegration(ctx context.Context, subject, list, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		row, e := t.integration(ctx, subject, list, id, "manage")
		if e != nil {
			return e
		}
		if row.Version != version {
			return ErrPreconditionFailed
		}
		if e = t.checkIntegrationLease(row); e != nil {
			return e
		}
		if e = t.supersedeIntegrationRefreshes(ctx, id); e != nil {
			return e
		}
		if _, e = t.Client.IntegrationItem.Update().Where(integrationitem.IntegrationIDEQ(id)).ClearIntegrationID().Save(ctx); e != nil {
			return e
		}
		if e = t.Client.ListIntegration.DeleteOne(row).Exec(ctx); e != nil {
			return e
		}
		c, e := t.Client.Resource.Get(ctx, list)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "integration.remove", c, map[string]any{"integration_id": id})
	})
}
func (s *Service) supersedeIntegrationRefreshes(ctx context.Context, id string) error {
	_, err := s.Client.ExecContext(ctx, `UPDATE operations SET status='superseded',completed_at=?,updated_at=? WHERE kind=? AND status='pending' AND json_extract(payload,'$.integration_id')=?`, time.Now().UTC(), time.Now().UTC(), OperationKindIntegrationRefresh, id)
	return err
}

type integrationRefreshPayload struct {
	IntegrationID string `json:"integration_id"`
}

func decodeIntegrationRefresh(raw json.RawMessage) (string, error) {
	var p integrationRefreshPayload
	if json.Unmarshal(raw, &p) != nil || p.IntegrationID == "" {
		return "", invalid("integration_id is required")
	}
	return p.IntegrationID, nil
}
func (s *Service) enqueueIntegrationRefresh(ctx context.Context, subject string, row *ent.ListIntegration) (*ent.Operation, error) {
	var pendingID string
	err := s.tx.QueryRowContext(ctx, `SELECT id FROM operations WHERE kind=? AND status='pending' AND json_extract(payload,'$.integration_id')=? AND created_by=? LIMIT 1`, OperationKindIntegrationRefresh, row.ID, subject).Scan(&pendingID)
	if err == nil {
		return s.Client.Operation.Get(ctx, pendingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Validate and enqueue through the operation registry for the caller's live
	// scopes and collection ACL. Deduplication never reveals another actor's job.
	payload, _ := json.Marshal(integrationRefreshPayload{row.ID})
	return s.EnqueueOperation(ctx, subject, row.ContainerID, OperationKindIntegrationRefresh, payload)
}
func (s *Service) RefreshIntegration(ctx context.Context, subject, list, id string) (out *ent.Operation, err error) {
	err = s.write(ctx, func(t *Service) error {
		row, e := t.integration(ctx, subject, list, id, "manage")
		if e != nil {
			return e
		}
		if row.Paused {
			return invalid("resume the integration before refreshing")
		}
		out, e = t.enqueueIntegrationRefresh(ctx, subject, row)
		return e
	})
	return
}
func validateIntegrationRefresh(ctx context.Context, t *Service, subject, list string, raw json.RawMessage) error {
	id, err := decodeIntegrationRefresh(raw)
	if err != nil {
		return err
	}
	row, err := t.integration(ctx, subject, list, id, "manage")
	if err != nil {
		return err
	}
	if row.Paused {
		return invalid("integration is paused")
	}
	if t.integrations == nil {
		return invalid("list integrations are not configured")
	}
	return nil
}
func integrationOperationHandler() OperationHandler {
	return OperationHandler{Scope: "list.write", Validate: validateIntegrationRefresh, ExternalStep: func(ctx context.Context, s *Service, op *ent.Operation) error {
		return s.refreshIntegrationOperation(ctx, op)
	}, Failed: func(ctx context.Context, t *Service, op *ent.Operation, cause error) error {
		id, err := decodeIntegrationRefresh(op.Payload)
		if err != nil {
			return nil
		}
		row, err := t.Client.ListIntegration.Get(ctx, id)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// The lease owner releases on both success and failure. An unrelated worker
		// must not clear a live lease when its duplicate job is superseded.
		return t.Client.ListIntegration.UpdateOne(row).SetError("Calendar refresh failed; existing events were retained. Check the feed and list schema.").Exec(ctx)
	}}
}

// QueueIntegrationRefreshes is used by the workspace's scheduled workflow.
// Each subscription rechecks its configuring user's credential and list Manage
// permission; one revoked credential does not stop other subscriptions.
func (s *Service) QueueIntegrationRefreshes(ctx context.Context, subject, workspace, id string) (int, error) {
	return s.queueIntegrationRefreshes(ctx, subject, workspace, id, false)
}
func (s *Service) QueueScheduledIntegrationRefreshes(ctx context.Context, workflowID string) (int, error) {
	w, err := s.Client.Workflow.Get(ctx, workflowID)
	if err != nil {
		return 0, err
	}
	if w.BuiltinKey == nil || *w.BuiltinKey != "integrations.refresh" || !w.Enabled || w.DeletedAt != nil {
		return 0, ErrForbidden
	}
	return s.queueIntegrationRefreshes(ctx, w.CreatedBy, w.WorkspaceID, "", true)
}
func (s *Service) queueIntegrationRefreshes(ctx context.Context, subject, workspace, id string, scheduled bool) (int, error) {
	n := 0
	err := s.write(ctx, func(t *Service) error {
		if !scheduled && id == "" {
			if _, err := t.authorize(ctx, subject, workspace, "manage"); err != nil {
				return err
			}
		}
		rows, err := t.Client.ListIntegration.Query().Where(listintegration.PausedEQ(false)).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if id != "" && row.ID != id {
				continue
			}
			c, err := t.Client.Resource.Get(ctx, row.ContainerID)
			if err != nil {
				return err
			}
			if c.WorkspaceID != workspace || c.DeletedAt != nil {
				continue
			}
			if !scheduled {
				if _, err = t.integration(ctx, subject, c.ID, row.ID, "manage"); err != nil {
					return err
				}
			}
			actor := row.ConfiguredBy
			credentialCtx := ctx
			if row.CredentialID != "" {
				credentialCtx = identity.WithRESTToken(ctx, identity.RESTTokenIdentity{Subject: actor, ID: row.CredentialID, Scopes: row.CredentialScopes})
				if identity.RequireCredential(credentialCtx, t.tx, actor) != nil {
					continue
				}
			}
			if _, err = t.integrationList(credentialCtx, actor, c.ID, "manage"); err != nil {
				continue
			}
			if _, err = t.enqueueIntegrationRefresh(credentialCtx, actor, row); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
func (s *Service) eventType(ctx context.Context, list string) (*ent.ContentType, error) {
	typ, err := s.Client.ContentType.Query().Where(contenttype.TemplateKeyEQ("event")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, invalid("the list needs the Event content type")
	}
	if err != nil {
		return nil, err
	}
	return s.contentType(ctx, list, typ.ID)
}
