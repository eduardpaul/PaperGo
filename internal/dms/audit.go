package dms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/directory"
	"time"
)

// AuditEvent is the API representation of an immutable audit record.
type AuditEvent struct {
	ID          string         `json:"id,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	ResourceID  string         `json:"resource_id,omitempty"`
	Subject     string         `json:"subject,omitempty"`
	Action      string         `json:"action,omitempty"`
	EntityType  string         `json:"entity_type,omitempty"`
	EntityID    string         `json:"entity_id,omitempty"`
	Properties  []string       `json:"properties,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	// Credential is the access token the action was taken with, such as an
	// AI assistant's connection, beside the person in Subject.
	Credential *AuditCredential `json:"credential,omitempty"`
}

// AuditCredential names the access token behind an audit event.
type AuditCredential struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type AuditQuery struct {
	Actor, EntityType, EntityID, WorkspaceID, After string
	From, To                                        *time.Time
	Limit                                           int
}
type auditCursor struct {
	At time.Time
	ID string
}

func (s *Service) OrganizationAudit(ctx context.Context, subject string, in AuditQuery) (Page[*AuditEvent], error) {
	return read(ctx, s, func(t *Service) (Page[*AuditEvent], error) {
		if err := t.RequireScope(ctx, subject, "audit.read"); err != nil {
			return Page[*AuditEvent]{}, err
		}
		if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
			return Page[*AuditEvent]{}, invalid("from must precede to")
		}
		filter := auditstore.Filter{Actor: in.Actor, EntityType: in.EntityType, EntityID: in.EntityID, WorkspaceID: in.WorkspaceID, From: in.From, To: in.To, Limit: pageSize(in.Limit) + 1}
		if in.After != "" {
			var cursor auditCursor
			raw, err := base64.RawURLEncoding.DecodeString(in.After)
			if err != nil || len(raw) > 256 || json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() {
				return Page[*AuditEvent]{}, invalid("invalid audit continuation")
			}
			if _, err = uuid.Parse(cursor.ID); err != nil {
				return Page[*AuditEvent]{}, invalid("invalid audit continuation")
			}
			filter.AfterAt, filter.AfterID = &cursor.At, cursor.ID
		}
		records, err := t.DAL.Audit().List(ctx, filter)
		if err != nil {
			return Page[*AuditEvent]{}, err
		}
		rows := make([]*AuditEvent, 0, len(records))
		names := map[string]string{}
		for _, record := range records {
			event := &AuditEvent{
				ID: record.ID, CreatedAt: record.CreatedAt, WorkspaceID: record.WorkspaceID, ResourceID: record.ResourceID,
				Subject: record.Subject, Action: record.Action, EntityType: record.EntityType, EntityID: record.EntityID, TraceID: record.TraceID,
				Properties: record.Properties, Details: record.Details,
			}
			if id := record.CredentialID; id != "" {
				name, known := names[id]
				if !known {
					tokens, err := t.DAL.Directory().AccessTokens(ctx, directory.AccessTokenFilter{ID: id})
					if err != nil {
						return Page[*AuditEvent]{}, err
					}
					if len(tokens) == 1 {
						name = tokens[0].Name
					}
					names[id] = name
				}
				event.Credential = &AuditCredential{ID: id, Name: name}
			}
			rows = append(rows, event)
		}
		return entityPage(rows, in.Limit, func(v *AuditEvent) string {
			raw, _ := json.Marshal(auditCursor{At: v.CreatedAt.UTC(), ID: v.ID})
			return base64.RawURLEncoding.EncodeToString(raw)
		}), nil
	})
}

// AuditTarget identifies the resource containing an already authorized action.
type AuditTarget struct {
	WorkspaceID, ResourceID, Kind string
}

// RecordEntity records an action in the current transaction. Callers authorize
// the action's scope before recording it; an audit write requires no extra grant.
func (s *Service) RecordEntity(ctx context.Context, subject, action string, target AuditTarget, details map[string]any) error {
	typ, id := auditEntity(action, target.Kind, target.ResourceID, details)
	_, err := s.DAL.Audit().Append(ctx, auditstore.Event{
		WorkspaceID: target.WorkspaceID, ResourceID: target.ResourceID, Subject: subject, Action: action,
		EntityType: typ, EntityID: id, TraceID: s.traceID, Properties: s.changedProperties(id, action), Details: details,
	})
	return err
}
