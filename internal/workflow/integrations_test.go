package workflow

import (
	"testing"

	"papergo/ent/workflow"
	"papergo/internal/dms"
	"papergo/internal/testutil"
)

func TestIntegrationWorkflowNeedsOnlyListManageAndRespectsDisable(t *testing.T) {
	ctx := t.Context()
	service := dms.NewService(testutil.Database(t).SQL)
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
	if err = service.Write(ctx, func(t *dms.Service) error { return flows.EnsureIntegrationWorkflow(ctx, t, "bob", list) }); err != nil {
		t.Fatal(err)
	}
	row, err := service.Client.Workflow.Query().Where(workflow.BuiltinKeyEQ(IntegrationRefreshBuiltIn)).Only(ctx)
	if err != nil || !row.Enabled {
		t.Fatal(row, err)
	}
	version, err := current(ctx, service, row)
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
	if err = service.Client.Workflow.UpdateOne(row).SetEnabled(false).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err = service.Write(ctx, func(t *dms.Service) error { return flows.EnsureIntegrationWorkflow(ctx, t, "bob", list) }); err != nil {
		t.Fatal(err)
	}
	rows, err := service.Client.Workflow.Query().Where(workflow.BuiltinKeyEQ(IntegrationRefreshBuiltIn)).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].Enabled {
		t.Fatal(rows, err)
	}
}
