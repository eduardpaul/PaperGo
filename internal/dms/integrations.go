package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/operations"
	"papergo/internal/identity"
	"papergo/internal/integration"
)

const IntegrationKindICalendar = "ical_https"
const OperationKindIntegrationRefresh = "integration.refresh"
const integrationLease = 2 * time.Minute

var ErrIntegrationBusy = errors.New("integration is refreshing; try again after it finishes")

type integrationRuntime struct {
	secrets *integration.Secrets
	ical    *iCalendarRuntime
}

// ConfigureIntegrations supplies the deployment key before serving requests.
func (s *Service) ConfigureIntegrations(secrets *integration.Secrets) {
	s.integrations = &integrationRuntime{secrets: secrets, ical: newICalendarRuntime()}
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
func (s *Service) integrationList(ctx context.Context, subject, id, action string) (*resources.Resource, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	if action == "manage" {
		if err := s.RequireScope(ctx, subject, "list.manage"); err != nil {
			return nil, err
		}
	}
	c, err := s.authorize(ctx, subject, id, action)
	if err != nil {
		return nil, err
	}
	if c.Kind != refs.KindList {
		return nil, invalid("integrations belong to ordinary lists")
	}
	return c, nil
}
func (s *Service) integration(ctx context.Context, subject, list, id, action string) (*integrations.Integration, error) {
	if _, err := s.integrationList(ctx, subject, list, action); err != nil {
		return nil, err
	}
	row, err := s.DAL.Integrations().Find(ctx, id)
	if dberr.IsNotFound(err) || err == nil && row.ContainerID != list {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
func (s *Service) ListIntegrations(ctx context.Context, subject, list string) ([]*IntegrationView, error) {
	return read(ctx, s, func(t *Service) ([]*IntegrationView, error) {
		if _, err := t.integrationList(ctx, subject, list, "read"); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Integrations().List(ctx, integrations.Filter{ContainerID: list})
		if err != nil {
			return nil, err
		}
		out := make([]*IntegrationView, len(rows))
		for i, row := range rows {
			out[i] = viewIntegration(row)
		}
		return out, nil
	})
}

// IntegrationView is a list integration as the API shows it. Its
// configuration, credential, refresh state and lease stay private.
type IntegrationView struct {
	ID            string     `json:"id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ContainerID   string     `json:"container_id"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	Paused        bool       `json:"paused"`
	Version       int        `json:"version"`
	ItemCount     int        `json:"item_count,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func viewIntegration(i integrations.Integration) *IntegrationView {
	return &IntegrationView{
		ID: i.ID, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ContainerID: i.ContainerID, Kind: i.Kind, Name: i.Name, Paused: i.Paused,
		Version: i.Version, ItemCount: i.ItemCount, LastAttemptAt: i.LastAttemptAt, LastSuccessAt: i.LastSuccessAt, Error: i.Error,
	}
}
func (s *Service) validateIntegrationURL(ctx context.Context, subject, list, raw string) (string, error) {
	if s.integrations == nil {
		return "", invalid("list integrations are not configured")
	}
	if _, err := read(ctx, s, func(t *Service) (*resources.Resource, error) { return t.integrationList(ctx, subject, list, "manage") }); err != nil {
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

// configuredBy records who configures an integration, limited to the caller's
// access token when one authenticated the request.
func configuredBy(ctx context.Context, subject string, i *integrations.Integration) {
	i.ConfiguredBy, i.CredentialID, i.CredentialScopes = subject, "", []string{}
	if token, limited := identity.AccessTokenContext(ctx); limited {
		i.CredentialID, i.CredentialScopes = token.ID, token.Scopes
	}
}
func (s *Service) CreateIntegration(ctx context.Context, subject, list string, in IntegrationInput) (out *IntegrationView, err error) {
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
		count, e := t.DAL.Integrations().Count(ctx, integrations.Filter{ContainerID: list})
		if e != nil {
			return e
		}
		if count >= 50 {
			return invalid("at most 50 integrations per list")
		}
		if _, e = t.ensureEventType(ctx, subject, c); e != nil {
			return e
		}
		source := integrations.Integration{ContainerID: list, Kind: IntegrationKindICalendar, Name: name, Generation: uuid.NewString(), ConfigCiphertext: cipher, ConfigHash: t.integrations.secrets.Hash(raw)}
		configuredBy(ctx, subject, &source)
		created, e := t.DAL.Integrations().Create(ctx, source)
		if e != nil {
			return e
		}
		out = viewIntegration(created)
		if _, e = t.enqueueIntegrationRefresh(ctx, subject, &created); e != nil {
			return e
		}
		return t.audit(ctx, subject, "integration.create", c, map[string]any{"integration_id": out.ID, "kind": in.Kind})
	})
	return
}
func (s *Service) checkIntegrationLease(row *integrations.Integration) error {
	if row.Leased(time.Now().UTC()) {
		return ErrIntegrationBusy
	}
	return nil
}
func (s *Service) UpdateIntegration(ctx context.Context, subject, list, id string, version int, in UpdateIntegration) (out *IntegrationView, err error) {
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
		if in.Name != nil {
			row.Name = *in.Name
		}
		if in.Paused != nil {
			row.Paused = *in.Paused
		}
		row.Version++
		configuredBy(ctx, subject, row)
		if in.Configuration != nil {
			row.ConfigCiphertext, row.ConfigHash, row.State, row.Error = cipher, t.integrations.secrets.Hash(raw), json.RawMessage(`{}`), ""
			if in.Reset {
				if e = t.DAL.Integrations().Detach(ctx, id); e != nil {
					return e
				}
				row.Generation, row.ItemCount, row.LastSuccessAt = uuid.NewString(), 0, nil
			}
		}
		if e = t.DAL.Integrations().Save(ctx, row); e != nil {
			return e
		}
		out = viewIntegration(*row)
		// Supersede queued work when its configuration changes. A new operation is
		// authorized with the configuring credential, without carrying the URL.
		if e = t.supersedeIntegrationRefreshes(ctx, id); e != nil {
			return e
		}
		if !out.Paused && (in.Configuration != nil || in.Paused != nil) {
			if _, e = t.enqueueIntegrationRefresh(ctx, subject, row); e != nil {
				return e
			}
		}
		c, e := t.DAL.Resources().Resource(ctx, list)
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
		if e = t.DAL.Integrations().Remove(ctx, id); e != nil {
			return e
		}
		c, e := t.DAL.Resources().Resource(ctx, list)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "integration.remove", c, map[string]any{"integration_id": id})
	})
}
func (s *Service) supersedeIntegrationRefreshes(ctx context.Context, id string) error {
	ended, err := s.DAL.Operations().Supersede(ctx, operations.Filter{Kinds: []string{OperationKindIntegrationRefresh}, PayloadField: "integration_id", PayloadValue: id}, time.Now())
	s.operationsFinished(ended...)
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
func (s *Service) enqueueIntegrationRefresh(ctx context.Context, subject string, row *integrations.Integration) (*OperationView, error) {
	pending, err := s.DAL.Operations().List(ctx, operations.Filter{Kinds: []string{OperationKindIntegrationRefresh}, Statuses: []operations.Status{operations.StatusPending}, PayloadField: "integration_id", PayloadValue: row.ID, CreatedBy: subject, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return viewOperation(pending[0], time.Now()), nil
	}
	// Validate and enqueue through the operation registry for the caller's live
	// scopes and collection ACL. Deduplication never reveals another actor's job.
	payload, _ := json.Marshal(integrationRefreshPayload{row.ID})
	return s.EnqueueOperation(ctx, subject, row.ContainerID, OperationKindIntegrationRefresh, payload)
}
func (s *Service) RefreshIntegration(ctx context.Context, subject, list, id string) (out *OperationView, err error) {
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
	return OperationHandler{Scope: "list.write", Validate: validateIntegrationRefresh, Execute: func(ctx context.Context, s *Service, op *operations.Operation) error {
		return s.refreshIntegrationOperation(ctx, op)
	}, Failed: func(ctx context.Context, t *Service, op *operations.Operation, cause error) error {
		id, err := decodeIntegrationRefresh(op.Payload)
		if err != nil {
			return nil
		}
		row, err := t.DAL.Integrations().Find(ctx, id)
		if dberr.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// The lease owner releases on both success and failure. An unrelated worker
		// must not clear a live lease when its duplicate job is superseded.
		row.Error = "Calendar refresh failed; existing events were retained. Check the feed and list schema."
		return t.DAL.Integrations().Save(ctx, &row)
	}}
}

// RefreshIntegrations queues a refresh of every active list integration. It
// is a maintenance task. Each refresh rechecks its configuring user's
// credential and list Manage permission; one revoked credential does not
// stop the others.
func (s *Service) RefreshIntegrations(ctx context.Context, _ time.Time) error {
	return s.write(ctx, func(t *Service) error {
		rows, err := t.DAL.Integrations().List(ctx, integrations.Filter{Unpaused: true})
		if err != nil {
			return err
		}
		for i := range rows {
			row := &rows[i]
			c, err := t.DAL.Resources().Resource(ctx, row.ContainerID)
			if err != nil {
				return err
			}
			if c.DeletedAt != nil {
				continue
			}
			actor := row.ConfiguredBy
			credentialCtx := ctx
			if row.CredentialID != "" {
				if credentialCtx, err = identity.RestoreCredential(ctx, dal.NewStore(t.db).WithTx(t.tx), actor, row.CredentialID, row.CredentialScopes); err != nil {
					continue
				}
			}
			if _, err = t.integrationList(credentialCtx, actor, c.ID, "manage"); err != nil {
				continue
			}
			if _, err = t.enqueueIntegrationRefresh(credentialCtx, actor, row); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Service) eventType(ctx context.Context, list string) (*catalog.ContentType, error) {
	typ, err := s.DAL.Catalog().OneContentType(ctx, catalog.ContentTypeFilter{TemplateKey: "event"})
	if dberr.IsNotFound(err) {
		return nil, invalid("the list needs the Event content type")
	}
	if err != nil {
		return nil, err
	}
	return s.contentType(ctx, list, typ.ID)
}
