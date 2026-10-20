package identity

import (
	"context"
	"github.com/google/uuid"
	"papergo/internal/dal"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/directory"
	"papergo/internal/requestmeta"
	"reflect"
	"sort"
)

func recordIdentityAudit(ctx context.Context, store *dal.Store, actor string, kind directory.EntityKind, change directory.Change, id string, properties ...string) error {
	ctx = requestmeta.EnsureTrace(ctx)
	if properties == nil {
		properties = []string{}
	}
	_, err := store.Audit().Append(ctx, audit.Event{
		ID: uuid.Must(uuid.NewV7()).String(), Subject: actor, Action: kind.AuditAction(change), EntityType: kind.AuditType(), EntityID: id,
		TraceID: requestmeta.Trace(ctx), Properties: properties,
	})
	return err
}

func auditedIdentity(ctx context.Context, store *dal.Store, actor string, typ directory.EntityKind, change directory.Change, id string, fn func() error) error {
	// The organization's stable ID is loaded inside its transaction.
	if typ == directory.EntityOrganization && id == "" {
		orgID, err := store.Directory().Organization(ctx)
		if err != nil {
			return err
		}
		id = orgID.ID
	}
	before, err := store.Directory().AuditState(ctx, typ, id)
	if err != nil {
		return err
	}
	if err = fn(); err != nil {
		return err
	}
	after, err := store.Directory().AuditState(ctx, typ, id)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	properties := []string{}
	if before == nil {
		change = directory.ChangeCreate
	} else if after == nil {
		change = directory.ChangeDelete
	} else {
		for name, value := range after {
			if !reflect.DeepEqual(before[name], value) {
				properties = append(properties, name)
			}
		}
		sort.Strings(properties)
	}
	return recordIdentityAudit(ctx, store, actor, typ, change, id, properties...)
}
