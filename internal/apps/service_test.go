package apps

import (
	"context"
	"errors"
	"strings"
	"testing"

	appstore "papergo/internal/dal/apps"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

const listApp = `import { app } from "@papergo/app";
export default app({
  parameters: {
    type: "object",
    required: ["list"],
    properties: { list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } } },
  },
  render: () => null,
});
`

func invalid(t *testing.T, err error, want string) {
	t.Helper()
	var v *dms.ValidationError
	if !errors.As(err, &v) || !strings.Contains(err.Error(), want) {
		t.Fatalf("want a validation error with %q: %v", want, err)
	}
}

// Those who may write apps add, change and delete them; the workspace's
// readers see the published ones without their source.
func TestAppsAreWrittenByAppManagersAndSeenByReaders(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	svc := &Service{DMS: s}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Diesel"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := s.Create(ctx, "alice", other.ID, dms.CreateResource{Kind: "list", Name: "Other list"})
	if err != nil {
		t.Fatal(err)
	}
	for subject, role := range map[string]string{"bob": "member", "carol": "visitor"} {
		if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: subject, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	in := Save{Name: "Diesel tracker", Source: listApp}

	if _, err = svc.Create(ctx, "alice", ws.ID, in); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("an owner without app.manage wrote an app: %v", err)
	}
	directory := identity.Directory{DB: db.SQL}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "App writers", Scopes: []string{Scope}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}

	draft, err := svc.Create(ctx, "alice", ws.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Key != "diesel-tracker" || draft.State != appstore.StateDraft || draft.Source != listApp || !draft.CanWrite || !strings.Contains(string(draft.ParametersSchema), `"collection"`) {
		t.Fatalf("created app: %+v", draft)
	}
	if _, err = svc.Get(ctx, "bob", draft.ID); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("a member saw a draft: %v", err)
	}
	publish := in
	publish.State = appstore.StatePublished
	_, err = svc.Update(ctx, "alice", draft.ID, draft.Version, publish)
	invalid(t, err, "choose the app's parameters before publishing it")
	publish.Parameters = map[string]any{"list": elsewhere.ID}
	_, err = svc.Update(ctx, "alice", draft.ID, draft.Version, publish)
	invalid(t, err, "parameters")
	publish.Parameters = map[string]any{"list": list.ID}
	published, err := svc.Update(ctx, "alice", draft.ID, draft.Version, publish)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != appstore.StatePublished || published.CurrentVersion != 1 || published.Version != draft.Version+1 || published.Parameters["list"] != list.ID {
		t.Fatalf("published app: %+v", published)
	}
	if _, err = svc.Update(ctx, "alice", draft.ID, draft.Version, publish); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatalf("a stale update: %v", err)
	}

	for _, reader := range []string{"bob", "carol"} {
		seen, err := svc.List(ctx, reader, ws.ID)
		if err != nil || len(seen) != 1 || seen[0].Source != "" || seen[0].CanWrite {
			t.Fatalf("%s's apps: %+v %v", reader, seen, err)
		}
		one, err := svc.Get(ctx, reader, draft.ID)
		if err != nil || one.Source != "" || one.Parameters["list"] != list.ID {
			t.Fatalf("%s reads the app: %+v %v", reader, one, err)
		}
		bundle, err := svc.Bundle(ctx, reader, draft.ID)
		if err != nil || !strings.Contains(bundle.Code, "__papergoAppDefinition") || len(bundle.Hash) != 64 || bundle.Number != 1 {
			t.Fatalf("%s reads the bundle: %+v %v", reader, bundle, err)
		}
		if _, err = svc.Update(ctx, reader, draft.ID, published.Version, publish); !errors.Is(err, dms.ErrForbidden) {
			t.Fatalf("%s changed the app: %v", reader, err)
		}
		if _, err = svc.Versions(ctx, reader, draft.ID); !errors.Is(err, dms.ErrForbidden) {
			t.Fatalf("%s read the versions: %v", reader, err)
		}
	}
	if _, err = svc.Get(ctx, "stranger", draft.ID); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("someone outside the workspace saw the app: %v", err)
	}
	if _, err = svc.List(ctx, "stranger", ws.ID); err == nil {
		t.Fatal("someone outside the workspace listed its apps")
	}

	broken := publish
	broken.Source = `export default app({ render: () => <div> });`
	_, err = svc.Update(ctx, "alice", draft.ID, published.Version, broken)
	invalid(t, err, "does not compile")
	changed := publish
	changed.Source = strings.Replace(listApp, "render: () => null", "render: () => \"Hello\"", 1)
	revised, err := svc.Update(ctx, "alice", draft.ID, published.Version, changed)
	if err != nil || revised.CurrentVersion != 2 {
		t.Fatalf("a new source: %+v %v", revised, err)
	}
	versions, err := svc.Versions(ctx, "alice", draft.ID)
	if err != nil || len(versions) != 2 || versions[0].Number != 2 || versions[1].Source != listApp {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	if _, err = svc.Create(ctx, "alice", ws.ID, in); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("a second app with the name: %v", err)
	}

	if err = svc.Delete(ctx, "alice", draft.ID, published.Version); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatalf("a stale delete: %v", err)
	}
	if err = svc.Delete(ctx, "bob", draft.ID, revised.Version); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("a member deleted the app: %v", err)
	}
	if err = svc.Delete(ctx, "alice", draft.ID, revised.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, "alice", draft.ID); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("a deleted app: %v", err)
	}
}
