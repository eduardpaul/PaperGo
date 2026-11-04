package apps

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// The launcher lists the apps of every workspace the caller reads, with
// the workspace's name: published apps, and drafts where they write apps.
func TestLauncherListsTheAppsOfEveryWorkspace(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	svc := &Service{DMS: s}
	directory := identity.Directory{DB: db.SQL}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "App writers", Scopes: []string{Scope}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	farm, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", farm.ID, dms.CreateResource{Kind: "list", Name: "Diesel"})
	if err != nil {
		t.Fatal(err)
	}
	office, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Office"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", farm.ID, dms.WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, "alice", farm.ID, Save{Name: "Diesel tracker", Source: listApp, State: appstore.StatePublished, Parameters: map[string]any{"list": list.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, "alice", farm.ID, Save{Name: "Barn draft", Source: listApp})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, "alice", office.ID, Save{Name: "Board", Source: `import { app } from "@papergo/app";
export default app({ render: () => null });`, State: appstore.StatePublished})
	if err != nil {
		t.Fatal(err)
	}

	names := func(subject string) []string {
		t.Helper()
		apps, err := svc.Launcher(ctx, subject)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, a := range apps {
			if a.Source != "" || a.ParametersSchema != nil {
				t.Fatalf("a launcher entry: %+v", a)
			}
			out = append(out, a.Name+" in "+a.WorkspaceName)
		}
		return out
	}
	if got := strings.Join(names("alice"), ", "); got != "Barn draft in Farm, Board in Office, Diesel tracker in Farm" {
		t.Fatalf("alice's launcher: %s", got)
	}
	if got := strings.Join(names("bob"), ", "); got != "Diesel tracker in Farm" {
		t.Fatalf("a visitor of the farm sees its published apps only: %s", got)
	}
	if got := names("stranger"); len(got) != 0 {
		t.Fatalf("someone in no workspace: %v", got)
	}
}

// A failure that says nothing about access stays a failure: a viewer is not
// told an app is missing, or that they may not write it, because the database
// could not answer.
func TestAFailedAccessCheckIsNotAnAnswer(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dms.Read(ctx, s, func(t2 *dms.Service) (struct{}, error) {
		if write, err := writes(ctx, t2, "stranger", ws.ID); err != nil || write {
			t.Errorf("no access is an answer: %v %v", write, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if write, err := writes(canceled, t2, "alice", ws.ID); !errors.Is(err, context.Canceled) || write {
			t.Errorf("a failed check: %v %v", write, err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Running a source can take seconds; other writes do not wait for it.
func TestCompilingAnAppHoldsUpNoWrites(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	svc := &Service{DMS: s}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	directory := identity.Directory{DB: db.SQL}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "App writers", Scopes: []string{Scope}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.Create(ctx, "alice", ws.ID, Save{Name: "Spins", Source: `import { app } from "@papergo/app"; while (true) {} export default app({ render: () => null });`})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err = s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Meanwhile"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		t.Fatalf("the write waited for the source to stop running: %v", err)
	default:
	}
	invalid(t, <-done, "too long")
}

// The launcher's queries do not grow with the apps or the workspaces.
func TestTheLauncherReadsInAFixedNumberOfQueries(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	meter := daltest.NewWriteMeter(t, db)
	s := dms.NewService(meter.SQL)
	svc := &Service{DMS: s}
	directory := identity.Directory{DB: db.SQL}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "App writers", Scopes: []string{Scope}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	add := func(name string) {
		t.Helper()
		ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range []string{name + " one", name + " two"} {
			if _, err = svc.Create(ctx, "alice", ws.ID, Save{Name: app, Source: `import { app } from "@papergo/app";
export default app({ render: () => null });`, State: appstore.StatePublished}); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(want int) int {
		t.Helper()
		meter.Start(t)
		apps, err := svc.Launcher(ctx, "alice")
		cost := meter.Stop(t)
		if err != nil || len(apps) != want {
			t.Fatalf("the launcher: %d apps, want %d: %v", len(apps), want, err)
		}
		return cost.Statements
	}
	add("First")
	few := read(2)
	for _, name := range []string{"Second", "Third", "Fourth"} {
		add(name)
	}
	if many := read(8); many != few {
		t.Fatalf("the launcher took %d statements for 2 apps and %d for 8", few, many)
	}
}
