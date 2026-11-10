package solutions

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// folder writes a solution's files to a folder on disk.
func folder(t *testing.T, files fstest.MapFS) string {
	t.Helper()
	dir := t.TempDir()
	for name, file := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The command line plans, applies, lists and checks solutions on a server,
// naming the workspace and the lists it binds by their names.
func TestCommandsTalkToAServer(t *testing.T) {
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	server := httptest.NewServer((&httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: admin}, Logger: slog.Default(), Modules: []httpapi.Module{Module(nil)}}).Handler())
	defer server.Close()
	ws, err := s.Create(t.Context(), admin, "", dms.CreateResource{Kind: "workspace", Name: "Casa"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := s.Create(t.Context(), admin, ws.ID, dms.CreateResource{Kind: "list", Name: "Depósito"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := Run(t.Context(), append(args, "--url", server.URL, "--token", token, "--workspace", "casa"), &out)
		return out.String(), err
	}
	dir := folder(t, tank())

	out, err := run("list")
	if err != nil || !strings.Contains(out, "no solution is installed") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = run("plan", dir)
	if err != nil || !strings.Contains(out, "tank 1.0.0 (first installation)") || !strings.Contains(out, "create  field tank_flag: choice") || !strings.Contains(out, "plan: 8 change(s); nothing was changed") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	out, err = run("apply", dir, "--set", "capacity_l=900", "--set", "time_zone=Europe/Madrid", "--bind", "readings=depósito", "--paused")
	if err != nil || !strings.Contains(out, "bind    collection readings: Depósito") || !strings.Contains(out, "applied: 8 change(s)") {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if in := installationOf(t, s, ws.ID, "tank"); in.Bindings["readings"] != mine.ID || !in.Paused || in.Settings["time_zone"] != "Europe/Madrid" {
		t.Fatalf("what the flags said was not applied: %+v", in)
	}
	out, err = run("apply", dir)
	if err != nil || !strings.Contains(out, "tank 1.0.0 (installed: 1.0.0)") || !strings.Contains(out, "applied: 0 change(s)") {
		t.Fatalf("apply again: %v\n%s", err, out)
	}
	out, err = run("list")
	if err != nil || !strings.Contains(out, "tank 1.0.0 (paused): Tank, applied ") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = run("status", "tank")
	if err != nil || !strings.Contains(out, "tank 1.0.0: has what it needs") {
		t.Fatalf("status: %v\n%s", err, out)
	}

	refused := folder(t, edit("solution.json", `"type": "datetime"`, `"type": "date"`))
	out, err = run("apply", refused)
	if err == nil || !strings.Contains(out, "REFUSED field tank_measured_at: is a datetime field") || !strings.Contains(err.Error(), "nothing was applied") {
		t.Fatalf("a refused apply: %v\n%s", err, out)
	}
	broken := folder(t, edit("solution.json", `{"key": "tank_flag"`, `{"key": "flag"`))
	if out, err = run("plan", broken); err == nil || !strings.Contains(out, `"flag" must start with "tank_"`) {
		t.Fatalf("a solution with problems is not sent: %v\n%s", err, out)
	}
	if out, err = run("apply", dir, "--bind", "readings=garage"); err == nil || !strings.Contains(err.Error(), `there is no list or library "garage"; there are: Depósito`) {
		t.Fatalf("an unknown list: %v\n%s", err, out)
	}
	// Pull writes the installation's files; remove uninstalls it.
	out2 := t.TempDir()
	if out, err = run("pull", "tank", "--out", out2); err != nil || !strings.Contains(out, "tank 1.0.0: 5 file(s) written") {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if out, err = run("plan", out2); err != nil || !strings.Contains(out, "plan: 0 change(s)") {
		t.Fatalf("what was pulled is what is installed: %v\n%s", err, out)
	}
	if out, err = run("remove", "tank"); err != nil || !strings.Contains(out, "delete  workflow tank-levels") || !strings.Contains(out, "removed; its lists, content types and fields stay") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if out, err = run("list"); err != nil || !strings.Contains(out, "no solution is installed") {
		t.Fatalf("list after remove: %v\n%s", err, out)
	}
	var said bytes.Buffer
	if err = Run(t.Context(), []string{"list", "--url", server.URL, "--token", "wrong-wrong-wrong-wrong-wrong-wrong", "--workspace", "Casa"}, &said); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a wrong token: %v", err)
	}
}

func installationOf(t *testing.T, s *dms.Service, workspaceID, key string) Installation {
	t.Helper()
	r, err := readInstalled(t.Context(), s, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return r.Installations[key]
}
