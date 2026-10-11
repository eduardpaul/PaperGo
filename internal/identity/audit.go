package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"papergo/internal/requestmeta"
	"reflect"
	"sort"
	"strings"
	"time"
)

func recordIdentityAudit(ctx context.Context, tx *sql.Tx, actor, action, entityType, id string, properties ...string) error {
	ctx = requestmeta.EnsureTrace(ctx)
	if properties == nil {
		properties = []string{}
	}
	raw, err := json.Marshal(properties)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_events(id,created_at,subject,action,entity_type,entity_id,properties,trace_id,details) VALUES(?,?,?,?,?,?,?,?, '{}')", uuid.Must(uuid.NewV7()).String(), time.Now().UTC(), actor, action, "identity."+entityType, id, string(raw), requestmeta.Trace(ctx))
	return err
}

// Only the affected directory entity is read. Credentials omit secrets and
// last-use stamps; group membership and role assignment changes are named.
func identityAuditState(ctx context.Context, tx *sql.Tx, typ, id string) (map[string]any, error) {
	queries := map[string]string{
		"user":         "SELECT display_name,email,active,deleted_at FROM identity_users WHERE id=?",
		"group":        "SELECT name,description,(SELECT group_concat(principal_type||':'||principal_id) FROM (SELECT principal_type,principal_id FROM identity_memberships WHERE group_id=identity_groups.id ORDER BY principal_type,principal_id)) AS members FROM identity_groups WHERE id=?",
		"role":         "SELECT name,description,scopes,(SELECT group_concat(principal_type||':'||principal_id) FROM (SELECT principal_type,principal_id FROM identity_assignments WHERE role_id=identity_roles.id ORDER BY principal_type,principal_id)) AS assignments FROM identity_roles WHERE id=?",
		"organization": "SELECT identifier,display_name,description FROM identity_organizations WHERE id=?",
		"rest_token":   "SELECT name,scopes,expires_at,revoked_at FROM identity_rest_tokens WHERE id=?",
	}
	query := queries[typ]
	if query == "" {
		return nil, errors.New("unknown audit entity")
	}
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range values {
		dest[i] = &values[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for i, name := range names {
		out[name] = values[i]
	}
	return out, nil
}

func auditedIdentity(ctx context.Context, tx *sql.Tx, actor, action, typ, id string, fn func() error) error {
	// The organization's stable ID is loaded inside its transaction.
	if typ == "organization" && id == "" {
		org, err := organization(ctx, tx)
		if err != nil {
			return err
		}
		id = org.ID
	}
	before, err := identityAuditState(ctx, tx, typ, id)
	if err != nil {
		return err
	}
	if err = fn(); err != nil {
		return err
	}
	after, err := identityAuditState(ctx, tx, typ, id)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	properties := []string{}
	if before == nil {
		action = strings.Split(action, ".")[0] + ".create"
	} else if after == nil {
		action = strings.Split(action, ".")[0] + ".delete"
	} else {
		for name, value := range after {
			if !reflect.DeepEqual(before[name], value) {
				properties = append(properties, name)
			}
		}
		sort.Strings(properties)
	}
	return recordIdentityAudit(ctx, tx, actor, action, typ, id, properties...)
}
