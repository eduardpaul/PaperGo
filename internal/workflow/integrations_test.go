package workflow

import (
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
)

func TestIntegrationWorkflowNeedsOnlyListManageAndRespectsDisable(t *testing.T) {
	ctx := t.Context()
	service := dms.NewService(daltest.Database(t).SQL)
	ws, err := service.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "List"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetPermissions(ctx, "alice", list.ID, list.Version, dms.Permissions{Inherit: false, Grants: []dms.Permission{{PrincipalType: "user", PrincipalID: "bob", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	flows := &Service{DMS: service}
	if err = service.Write(ctx, func(t *dms.Service) error { return flows.EnsureIntegrationWorkflow(ctx, t, "bob", list.ID) }); err != nil {
		t.Fatal(err)
	}
	found, err := service.DAL.Workflows().List(ctx, workflows.Filter{BuiltinKey: IntegrationRefreshBuiltIn})
	if err != nil || len(found) != 1 || !found[0].AutomaticEnabled {
		t.Fatal(found, err)
	}
	row := found[0]
	version, err := service.DAL.Workflows().Version(ctx, row.ID, int64(row.CurrentVersion))
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseDefinition(version.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = def.validate(); err != nil {
		t.Fatal(err)
	}
	if len(def.Triggers) != 1 || def.Triggers[0].Cron != "*/15 * * * *" {
		t.Fatal(def)
	}
	if _, _, err = service.DAL.Workflows().Revise(ctx, row.ID, row.Version, row.UpdatedBy, version.Definition, workflows.Changes{AutomaticEnabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	if err = service.Write(ctx, func(t *dms.Service) error { return flows.EnsureIntegrationWorkflow(ctx, t, "bob", list.ID) }); err != nil {
		t.Fatal(err)
	}
	rows, err := service.DAL.Workflows().List(ctx, workflows.Filter{BuiltinKey: IntegrationRefreshBuiltIn})
	if err != nil || len(rows) != 1 || rows[0].AutomaticEnabled {
		t.Fatal(rows, err)
	}
}
