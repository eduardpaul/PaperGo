package dms

import (
	"context"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/identity"
	"sort"
)

func (s *Service) DeleteUser(ctx context.Context, actor, subject string) error {
	return s.write(ctx, func(t *Service) error {
		return identity.DeleteUserInTx(ctx, t.DAL, actor, subject, func() error {
			return t.purgePersonalHome(ctx, actor, subject)
		})
	})
}

// Account deletion is the only operation that purges protected Home resources.
// Audit records retain IDs; content and execution payloads are removed.
func (s *Service) purgePersonalHome(ctx context.Context, actor, subject string) error {
	w, err := s.DAL.Resources().One(ctx, resources.Filter{PersonalOwnerID: subject})
	if dberr.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{WorkspaceID: w.ID})
	if err != nil {
		return err
	}
	links, err := s.DAL.Relationships().List(ctx, relationships.Filter{Workspace: w.ID})
	if err != nil {
		return err
	}
	for _, link := range links {
		if err = s.touchRelationship(ctx, actor, link, "deleted"); err != nil {
			return err
		}
	}
	runRows, err := s.DAL.Runs().List(ctx, runstore.Filter{WorkspaceID: w.ID})
	if err != nil {
		return err
	}
	purged := make([]string, len(runRows))
	for i, run := range runRows {
		purged[i] = run.ID
		if _, err = s.DAL.Audit().Append(ctx, auditstore.Event{WorkspaceID: w.ID, Subject: actor, Action: "run.purge", EntityType: "workflow_run", EntityID: run.ID, TraceID: s.traceID}); err != nil {
			return err
		}
	}
	// A run's operation holds its input and journal; deleting it deletes
	// the run with them.
	if err = s.DAL.Operations().Delete(ctx, purged); err != nil {
		return err
	}
	if err = s.DAL.Resources().ClearWorkspace(ctx, w.ID, actor); err != nil {
		return err
	}
	// Delete descendants before their parent to respect protected containment.
	byID := map[string]*resources.Resource{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	depth := func(row *resources.Resource) int {
		n := 0
		for row.ParentID != nil && n < len(rows) {
			row = byID[*row.ParentID]
			if row == nil {
				break
			}
			n++
		}
		return n
	}
	sort.Slice(rows, func(i, j int) bool { return depth(rows[i]) > depth(rows[j]) })
	for _, row := range rows {
		if err = s.audit(ctx, actor, "resource.purge", row, nil); err != nil {
			return err
		}
		if err = s.DAL.Resources().Purge(ctx, row.ID); err != nil {
			return err
		}
		if contentKind(row.Kind) {
			s.emit(ctx, EventItemPurged, actor, row.WorkspaceID, row.ContainerID, row.ID, nil)
		}
	}

	return nil
}
