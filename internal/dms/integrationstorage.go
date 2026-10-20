package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/internal/dal/resources"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/revisions"
)

// IntegrationData is opaque provider data frozen with an ordinary item revision.
// The core never interprets its payload or promotes provider-specific columns.
type IntegrationData struct {
	ProviderKind string
	Data         json.RawMessage
}

// SetIntegrationData supplies provider data for the next item revision in this
// transaction. Providers use ordinary DMS creates/updates for content writes.
func (s *Service) SetIntegrationData(kind string, data json.RawMessage) error {
	if !s.transaction || !operationKind.MatchString(kind) || len(data) > 1<<20 || !json.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return invalid("integration data requires a provider kind, object within 1 MiB, and a write transaction")
	}
	s.pendingIntegrationData = &IntegrationData{kind, append(json.RawMessage(nil), data...)}
	return nil
}
func (s *Service) revisionIntegrationData(ctx context.Context, revisionID string) (*integrations.RevisionData, error) {
	return s.DAL.Integrations().RevisionData(ctx, revisionID)
}
func (s *Service) recordIntegrationData(ctx context.Context, rev, previous *revisions.Revision) error {
	data := s.pendingIntegrationData
	s.pendingIntegrationData = nil
	if data == nil && previous != nil && previous.ContentTypeID == rev.ContentTypeID {
		old, err := s.revisionIntegrationData(ctx, previous.ID)
		if err != nil {
			return err
		}
		if old != nil {
			data = &IntegrationData{old.ProviderKind, old.Data}
		}
	}
	if data == nil || data.ProviderKind == "" {
		return nil
	}
	return s.DAL.Integrations().SaveRevisionData(ctx, rev.ID, data.ProviderKind, data.Data)
}
func (s *Service) integrationManaged(ctx context.Context, id string) (bool, error) {
	row, err := s.DAL.Integrations().Item(ctx, id)
	if dberr.IsNotFound(err) {
		return false, nil
	}
	return err == nil && row.IntegrationID != "", err
}
func (s *Service) protectIntegrationFields(ctx context.Context, r *resources.Resource, in UpdateResource) error {
	if s.integrationWrite {
		return nil
	}
	row, err := s.DAL.Integrations().Item(ctx, r.ID)
	if dberr.IsNotFound(err) || err == nil && row.IntegrationID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	for _, key := range row.ManagedFields {
		switch key {
		case "$name":
			if in.Name != nil && *in.Name != r.Name {
				return invalid("item title is managed by its list integration")
			}
		case "$content_type":
			if in.ContentTypeID != nil && (r.ContentTypeID == nil || *in.ContentTypeID != *r.ContentTypeID) {
				return invalid("item content type is managed by its list integration")
			}
		default:
			if in.Values != nil {
				if value, changed := (*in.Values)[key]; changed && !sameJSON(value, r.Values[key]) {
					return invalid("item field is managed by its list integration: " + key)
				}
			}
		}
	}
	return nil
}

type ItemIntegration struct {
	IntegrationID *string  `json:"integration_id"`
	Name          string   `json:"name,omitempty"`
	ManagedFields []string `json:"managed_fields"`
}

func (s *Service) ItemIntegration(ctx context.Context, subject, id string) (ItemIntegration, error) {
	return read(ctx, s, func(t *Service) (ItemIntegration, error) {
		out := ItemIntegration{ManagedFields: []string{}}
		if _, err := t.GetSurface(ctx, subject, id, "auto"); err != nil {
			return out, err
		}
		row, err := t.DAL.Integrations().Item(ctx, id)
		if dberr.IsNotFound(err) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if row.IntegrationID != "" {
			source, err := t.DAL.Integrations().Find(ctx, row.IntegrationID)
			if err != nil {
				return out, err
			}
			out.IntegrationID = &row.IntegrationID
			out.Name = source.Name
			out.ManagedFields = append([]string(nil), row.ManagedFields...)
		}
		return out, nil
	})
}

// restoreIntegrationData restores opaque provider data with local revision
// content. Active integrations retain ownership of their current source data.
func (s *Service) restoreIntegrationData(ctx context.Context, revisionID, itemID string) error {
	managed, err := s.integrationManaged(ctx, itemID)
	if err != nil || managed {
		return err
	}
	data, err := s.revisionIntegrationData(ctx, revisionID)
	if err != nil {
		return err
	}
	if data == nil {
		s.pendingIntegrationData = &IntegrationData{}
	} else {
		s.pendingIntegrationData = &IntegrationData{data.ProviderKind, data.Data}
	}
	return nil
}
