package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"strings"
	"testing"
	"time"
)

func TestRESTFilteredBulkUpdateProgressFailuresAndStrictInput(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Bulk"})
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateField(ctx, "alice", l.ID, dms.CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true}); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"A", "B"} {
		if _, e = s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: name, Values: map[string]any{"status": "todo"}}); e != nil {
			t.Fatal(e)
		}
	}
	path := "/v1/resources/" + l.ID + "/bulk-update"
	body := `{"query":{"filter":{"field":"status","value":"todo"}},"values":{"status":"done"}}`
	if res := request(h, "POST", path, body, "", "", "application/json"); res.Code != 401 {
		t.Fatal(res.Code)
	}
	for _, bad := range []string{`{"values":{"status":"done"},"mode":"partial"}`, `{"values":{"status":"done"}} {}`} {
		res := request(h, "POST", path, bad, testToken, "", "application/json")
		if res.Code != 400 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	for _, bad := range []string{`{}`, `{"values":{}}`, `{"query":{"sort":{"field":"$name"}},"values":{"status":"done"}}`, `{"query":{"filter":{"field":"bad","value":1}},"values":{"status":"done"}}`} {
		res := request(h, "POST", path, bad, testToken, "", "application/json")
		if res.Code != 422 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	res := request(h, "POST", path, body, testToken, "", "application/json")
	var op ent.Operation
	if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil || op.Status != "pending" || res.Header().Get("Location") != "/v1/operations/"+op.ID {
		t.Fatal(res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "payload") || strings.Contains(res.Body.String(), "prepared") {
		t.Fatal("internal payload exposed", res.Body.String())
	}
	workerCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { s.RunOperations(workerCtx, slog.New(slog.NewTextHandler(io.Discard, nil))); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })
	wait := func(id string) ent.Operation {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			res := request(h, "GET", "/v1/operations/"+id, "", testToken, "", "")
			var got ent.Operation
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &got) != nil {
				t.Fatal(res.Code, res.Body.String())
			}
			if got.Status == "succeeded" || got.Status == "failed" {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatal("operation did not finish", got)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	got := wait(op.ID)
	if got.Status != "succeeded" || got.Matched != 2 || got.Updated != 2 || got.Failed != 0 || got.Processed != 2 || got.Progress != 100 {
		t.Fatal(got)
	}
	res = request(h, "POST", path, `{"values":{"unknown":true}}`, testToken, "", "application/json")
	if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	got = wait(op.ID)
	if got.Matched != 2 || got.Updated != 0 || got.Failed != 2 || len(got.Failures) != 2 || got.Failures[0].Reason == "" || got.Progress != 100 {
		t.Fatal(got)
	}
	if e = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); e != nil {
		t.Fatal(e)
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	res = request(bob, "GET", "/v1/operations/"+op.ID, "", testToken, "", "")
	if res.Code != 404 {
		t.Fatal("another user read failures", res.Code, res.Body.String())
	}
	res = request(bob, "GET", "/v1/resources/"+l.ID+"/operations", "", testToken, "", "")
	if res.Code != 200 || strings.Contains(res.Body.String(), op.ID) {
		t.Fatal("another user listed failures", res.Code, res.Body.String())
	}
}
