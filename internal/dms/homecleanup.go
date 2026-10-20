package dms

import (
	"context"
	"database/sql"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/identity"
	"sort"
)

func (s *Service) DeleteUser(ctx context.Context, actor, subject string) error {
	var purged []string
	err := s.write(ctx, func(t *Service) error {
		return identity.DeleteUserInTx(ctx, t.DAL, actor, subject, func() (err error) {
			purged, err = t.purgePersonalHome(ctx, actor, subject)
			return err
		})
	})
	// Payloads written while the purge ran are removed once it committed.
	if err == nil && !s.transaction && s.runSealer != nil {
		err = s.runSealer(ctx, purged)
	}
	return err
}

// Account deletion is the only operation that purges protected Home resources.
// Audit records retain IDs; content and execution payloads are removed. It
// returns the purged runs.
func (s *Service) purgePersonalHome(ctx context.Context, actor, subject string) ([]string, error) {
	w, err := s.DAL.Resources().One(ctx, resources.Filter{PersonalOwnerID: subject})
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{WorkspaceID: w.ID})
	if err != nil {
		return nil, err
	}
	links, err := s.DAL.Relationships().List(ctx, relationships.Filter{Workspace: w.ID})
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if err = s.touchRelationship(ctx, actor, link, "deleted"); err != nil {
			return nil, err
		}
	}
	runRows, err := s.DAL.Runs().List(ctx, runstore.Filter{WorkspaceID: w.ID})
	if err != nil {
		return nil, err
	}
	purged := make([]string, len(runRows))
	for i, run := range runRows {
		purged[i] = run.ID
		if _, err = s.DAL.Audit().Append(ctx, auditstore.Event{WorkspaceID: w.ID, Subject: actor, Action: "run.purge", EntityType: "workflow_run", EntityID: run.ID, TraceID: s.traceID}); err != nil {
			return nil, err
		}
	}
	if s.runPurger != nil {
		if err = s.runPurger(ctx, s.tx, w.ID); err != nil {
			return nil, err
		}
	}
	if err = s.DAL.Resources().ClearWorkspace(ctx, w.ID, actor); err != nil {
		return nil, err
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
			return nil, err
		}
		if err = s.DAL.Resources().Purge(ctx, row.ID); err != nil {
			return nil, err
		}
		if contentKind(row.Kind) {
			s.emit(ctx, EventItemPurged, actor, row.WorkspaceID, row.ContainerID, row.ID, nil)
		}
	}

	return purged, nil
}

// RegisterRunPurger attaches the runner's runtime-payload cleanup: purge
// runs in the transaction that purges a workspace's runs, seal after it
// committed. Register it before serving requests.
func (s *Service) RegisterRunPurger(purge func(context.Context, *sql.Tx, string) error, seal func(context.Context, []string) error) {
	s.runPurger, s.runSealer = purge, seal
}
