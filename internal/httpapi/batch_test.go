package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
)

func TestBatchUsesOuterAdmissionSlot(t *testing.T) {
	_, service, path := setup(t)
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{DMS: service, Storage: store, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1}
	w := request(a.Handler(), "POST", "/v1/$batch", `{"requests":[{"id":"one","method":"GET","url":"workspaces"}]}`, testToken, "", "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":200`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBatchIndependentCallsAndDependencies(t *testing.T) {
	h, service, _ := setup(t)
	body := `{"requests":[
		{"id":"create","method":"POST","url":"workspaces","body":{"name":"Batch workspace"}},
		{"id":"missing","method":"GET","url":"/v1/resources/00000000-0000-0000-0000-000000000001"},
		{"id":"skip","method":"POST","url":"workspaces","dependsOn":["missing"],"body":{"name":"Skipped"}},
		{"id":"skip-again","method":"GET","url":"workspaces","dependsOn":["skip"]},
		{"id":"unauthenticated","method":"GET","url":"workspaces","headers":{"Authorization":"Bearer wrong"}},
		{"id":"list","method":"GET","url":"/workspaces","dependsOn":["create"]}
	]}`
	w := request(h, "POST", "/v1/$batch", body, testToken, "", "application/json")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Responses []batchResponse `json:"responses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := []int{201, 404, 424, 424, 401, 200}
	if len(out.Responses) != len(want) {
		t.Fatal(w.Body.String())
	}
	ids := map[string]bool{}
	for i, result := range out.Responses {
		if result.Status != want[i] {
			t.Fatalf("call %d: %+v", i, result)
		}
		if result.Status != 424 {
			id := result.Headers["X-Request-ID"]
			if id == "" || ids[id] || id == w.Header().Get("X-Request-ID") {
				t.Fatal("subrequests need independent request IDs")
			}
			ids[id] = true
		}
	}
	if out.Responses[0].Headers["ETag"] != `"1"` || out.Responses[0].Headers["Location"] == "" {
		t.Fatal(out.Responses[0])
	}
	page, err := service.Browse(context.Background(), "alice", dms.Browse{Limit: 50})
	if err != nil || len(page.Data) != 1 {
		t.Fatal("batch must preserve independent successful writes and skip dependencies", page, err)
	}
}

func TestBatchValidatesBeforeExecution(t *testing.T) {
	h, service, _ := setup(t)
	for _, call := range []string{
		`{"id":"ok","method":"GET","url":"workspaces"}`,
		`{"id":"bad","method":"GET","url":"https://example.com/v1/workspaces"}`,
		`{"id":"bad","method":"GET","url":"/v1/%24batch"}`,
		`{"id":"bad","method":"GET","url":"/v1/%2e%2e/workspaces"}`,
		`{"id":"bad","method":"GET","url":"workspaces","dependsOn":["future"]}`,
		`{"id":"bad","method":"TRACE","url":"workspaces"}`,
		`{"id":"bad","method":"GET","url":"workspaces","headers":{"Host":"other"}}`,
	} {
		body := `{"requests":[{"id":"ok","method":"POST","url":"workspaces","body":{"name":"Must not exist"}},` + call + `]}`
		w := request(h, "POST", "/v1/$batch", body, testToken, "", "application/json")
		if w.Code != 400 {
			t.Fatal(call, w.Code, w.Body.String())
		}
	}
	page, err := service.Browse(context.Background(), "alice", dms.Browse{Limit: 50})
	if err != nil || len(page.Data) != 0 {
		t.Fatal(page, err)
	}
	for _, body := range []string{`{"requests":[]}`, `{"requests":` + strings.Repeat(" ", 4) + `null}`} {
		if w := request(h, "POST", "/v1/$batch", body, testToken, "", "application/json"); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "POST", "/v1/$batch", `{"requests":[]}`, "", "", "application/json"); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestBatchMixedContentAndRead(t *testing.T) {
	h, service, _ := setup(t)
	ctx := context.Background()
	workspace, err := service.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	library, err := service.Create(ctx, "alice", workspace.ID, dms.CreateResource{Kind: "library", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Create(ctx, "alice", library.ID, dms.CreateResource{Kind: "item", Name: "Text"})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"requests":[{"id":"upload","method":"PUT","url":"items/%s/content","headers":{"If-Match":"\"1\"","Content-Type":"text/plain","X-Filename":"note.pdf"},"body":"%%PDF-hello"},{"id":"download","method":"GET","url":"items/%s/content","dependsOn":["upload"]}]}`, item.ID, item.ID)
	w := request(h, http.MethodPost, "/v1/$batch", body, testToken, "", "application/json")
	var out struct {
		Responses []batchResponse `json:"responses"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(out.Responses) != 2 || out.Responses[0].Status != 201 || out.Responses[1].Status != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	encoded, ok := out.Responses[1].Body.(string)
	if !ok {
		t.Fatal("binary response must be base64", out.Responses[1].Body)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "%PDF-hello" || out.Responses[1].Headers["Content-Type"] != "application/pdf" {
		t.Fatal("binary download", string(decoded), err, out.Responses[1].Headers)
	}

}
