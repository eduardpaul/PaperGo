package dms

import (
	"context"
	"database/sql"
	"papergo/ent"
	"papergo/ent/relationship"
	"papergo/ent/resource"
	"papergo/ent/workflowrun"
	"papergo/internal/identity"
	"sort"
	"time"
)

func (s *Service) DeleteUser(ctx context.Context, actor, subject string) error {
	return s.write(ctx, func(t *Service) error {
		return identity.DeleteUserInTx(ctx, t.tx, actor, subject, func() error { return t.purgePersonalHome(ctx, actor, subject) })
	})
}

// Account deletion is the only operation that purges protected Home resources.
// Audit records retain IDs; content and execution payloads are removed.
func (s *Service) purgePersonalHome(ctx context.Context, actor, subject string) error {
	w, err := s.Client.Resource.Query().Where(resource.PersonalOwnerIDEQ(subject)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := s.Client.Resource.Query().Where(resource.WorkspaceIDEQ(w.ID)).All(ctx)
	if err != nil {
		return err
	}
	links, err := s.Client.Relationship.Query().Where(relationship.Or(relationship.HasSourceWith(resource.WorkspaceIDEQ(w.ID)), relationship.HasTargetWith(resource.WorkspaceIDEQ(w.ID)))).All(ctx)
	if err != nil {
		return err
	}
	for _, link := range links {
		if err = s.touchRelationship(ctx, actor, link, "deleted"); err != nil {
			return err
		}
	}
	if _, err = s.tx.ExecContext(ctx, "DELETE FROM relationships WHERE source_id IN (SELECT id FROM resources WHERE workspace_id=?) OR target_id IN (SELECT id FROM resources WHERE workspace_id=?)", w.ID, w.ID); err != nil {
		return err
	}
	runRows, err := s.Client.WorkflowRun.Query().Where(workflowrun.WorkspaceIDEQ(w.ID)).All(ctx)
	if err != nil {
		return err
	}
	for _, run := range runRows {
		if err = s.Client.AuditEvent.Create().SetSubject(actor).SetAction("run.purge").SetEntityType("workflow_run").SetEntityID(run.ID).SetWorkspaceID(w.ID).SetProperties([]string{}).SetTraceID(s.traceID).SetDetails(map[string]any{}).Exec(ctx); err != nil {
			return err
		}
	}
	if s.runPurger != nil {
		if err = s.runPurger(ctx, s.tx, w.ID); err != nil {
			return err
		}
	}
	// Tombstones release retention and last-owner guards during this transaction.
	now := time.Now().UTC()
	if _, err = s.tx.ExecContext(ctx, "UPDATE resources SET deleted_at=?,deleted_by=?,name_key=NULL,head_revision_id=NULL,published_revision_id=NULL,schema_head_id=NULL WHERE workspace_id=?", now, actor, w.ID); err != nil {
		return err
	}
	if _, err = s.tx.ExecContext(ctx, "INSERT INTO blob_deletions(id,created_at,object_key) SELECT b.id,?,b.object_key FROM blobs b JOIN resources r ON r.id=b.item_id WHERE r.workspace_id=? ON CONFLICT(object_key) DO NOTHING", now, w.ID); err != nil {
		return err
	}
	sub := "SELECT id FROM resources WHERE workspace_id=?"
	runs := "SELECT id FROM workflow_runs WHERE workspace_id=?"
	statements := []string{
		"DELETE FROM runner_step_results WHERE run_id IN (" + runs + ")",
		"DELETE FROM workflow_run_items WHERE run_id IN (" + runs + ")",
		"DELETE FROM workflow_runs WHERE workspace_id=?",
		"DELETE FROM workflow_triggers WHERE workspace_id=?",
		"DELETE FROM workflow_versions WHERE workflow_id IN (SELECT id FROM workflows WHERE workspace_id=?)",
		"DELETE FROM workflows WHERE workspace_id=?",
		"DELETE FROM domain_events WHERE workspace_id=?",
		"DELETE FROM operation_items WHERE item_id IN (" + sub + ")",
		"DELETE FROM operations WHERE container_id IN (" + sub + ")",
		"DELETE FROM sync_tokens WHERE view_id IN (SELECT id FROM sync_views WHERE container_id IN (" + sub + "))",
		"DELETE FROM sync_states WHERE view_id IN (SELECT id FROM sync_views WHERE container_id IN (" + sub + "))",
		"DELETE FROM sync_views WHERE container_id IN (" + sub + ")",
		"DELETE FROM group_inboxes WHERE library_id IN (" + sub + ")",
		"DELETE FROM smart_folders WHERE workspace_id=?",
		"DELETE FROM schema_templates WHERE workspace_id=?",
		"DELETE FROM list_views WHERE container_id IN (" + sub + ")",
		"DELETE FROM field_values WHERE item_id IN (" + sub + ")",
		"DELETE FROM business_keys WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_surfaces WHERE item_id IN (" + sub + ")",
		"DELETE FROM publications WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_histories WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_revisions WHERE item_id IN (" + sub + ")",
		"DELETE FROM blobs WHERE item_id IN (" + sub + ")",
		"DELETE FROM grants WHERE resource_id IN (" + sub + ")",
		"DELETE FROM collection_types WHERE container_id IN (" + sub + ")",
		"DELETE FROM schema_revisions WHERE container_id IN (" + sub + ")",
	}
	for _, statement := range statements {
		if _, err = s.tx.ExecContext(ctx, statement, w.ID); err != nil {
			return err
		}
	}
	// Delete descendants before their parent to respect protected containment.
	byID := map[string]*ent.Resource{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	depth := func(row *ent.Resource) int {
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
		if err = s.Client.Resource.DeleteOneID(row.ID).Exec(ctx); err != nil {
			return err
		}
		if contentKind(row.Kind) {
			s.emit(ctx, EventItemPurged, actor, row.WorkspaceID, row.ContainerID, row.ID, nil)
		}
	}

	return nil
}

// RegisterRunPurger attaches the runner's transactional runtime-payload cleanup.
// Register it before serving requests.
func (s *Service) RegisterRunPurger(purge func(context.Context, *sql.Tx, string) error) {
	s.runPurger = purge
}
