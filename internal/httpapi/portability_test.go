package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPortabilityHTTPExportDownloadAndAuthorization(t *testing.T) {
	_, s, path := setup(t)
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	a := &API{DMS: s, Storage: store, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := a.Handler()
	response := request(handler, "POST", "/v1/portability/exports", "", testToken, "", "")
	if response.Code != 202 || !strings.HasPrefix(response.Header().Get("Location"), "/v1/operations/") {
		t.Fatal(response.Code, response.Body.String())
	}
	var op dms.OperationView
	if err = json.Unmarshal(response.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = worker.Single(s, a.Logger).Run(ctx) }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 250; i++ {
		current, err := s.Operation(t.Context(), "organization-admin", op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != "pending" {
			op = *current
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if op.Status != "succeeded" {
		t.Fatal(op.Status, op.Error)
	}
	response = request(handler, "GET", "/v1/portability/exports/"+op.ID+"/package", "", testToken, "", "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" || response.Header().Get("ETag") == "" {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, err = zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len())); err != nil {
		t.Fatal(err)
	}
	denied := (&API{DMS: s, Storage: store, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: a.Logger}).Handler()
	response = request(denied, "GET", "/v1/portability/exports/"+op.ID+"/package", "", testToken, "", "")
	if response.Code != 403 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(handler, "POST", "/v1/portability/imports", "not a zip", testToken, "", "text/plain")
	if response.Code != 415 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestPortabilityHTTPWorkspaceConfigurationCopy(t *testing.T) {
	ctx := t.Context()
	_, source, path := setup(t)
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	ws, err := source.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := source.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Not included"}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := (&API{DMS: source, Storage: store, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: logger}).Handler()
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = worker.Single(source, logger).Run(workerCtx) }()
	defer func() { cancel(); <-done }()
	body := `{"workspace_id":"` + ws.ID + `","include_data":false}`
	response := request(handler, "POST", "/v1/portability/exports", body, testToken, "", "application/json")
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var queued dms.OperationView
	if err = json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	waitPackageHTTPTest(t, source, queued.ID)
	downloaded := request(handler, "GET", "/v1/portability/exports/"+queued.ID+"/package", "", testToken, "", "")
	if downloaded.Code != 200 {
		t.Fatal(downloaded.Code, downloaded.Body.String())
	}
	_, target, targetPath := setup(t)
	targetStore, err := storage.NewLocal(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.RegisterPortability(targetStore); err != nil {
		t.Fatal(err)
	}
	existing, err := target.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Already here"})
	if err != nil {
		t.Fatal(err)
	}
	targetHandler := (&API{DMS: target, Storage: targetStore, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: logger}).Handler()
	targetCtx, targetCancel := context.WithCancel(ctx)
	targetDone := make(chan struct{})
	go func() { defer close(targetDone); _ = worker.Single(target, logger).Run(targetCtx) }()
	defer func() { targetCancel(); <-targetDone }()
	for _, dry := range []bool{true, false} {
		response = request(targetHandler, "POST", "/v1/portability/imports?dry_run="+strconv.FormatBool(dry)+"&workspace_name=Copied%20via%20API", downloaded.Body.String(), testToken, "", "application/zip")
		if response.Code != 202 {
			t.Fatal(response.Code, response.Body.String())
		}
		if err = json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
			t.Fatal(err)
		}
		op := waitPackageHTTPTest(t, target, queued.ID)
		var result dms.PackageResult
		if err = json.Unmarshal(op.Result, &result); err != nil || !result.Valid || result.DryRun != dry || result.WorkspaceName != "Copied via API" {
			t.Fatal(result, err)
		}
		expected := 1
		if !dry {
			expected = 3
		}
		if count, _ := target.DAL.Resources().Count(ctx, resources.Filter{}); count != expected {
			t.Fatal("unexpected resources", count, expected)
		}
	}
	if _, err = target.Get(ctx, "alice", existing.ID); err != nil {
		t.Fatal("existing workspace changed", err)
	}
	for _, body := range []string{`{"include_data":"false"}`, `{"workspace_id":"missing"}`, `{"include_content":true}`} {
		response = request(handler, "POST", "/v1/portability/exports", body, testToken, "", "application/json")
		if response.Code < 400 || response.Code >= 500 {
			t.Fatal("invalid export options accepted", response.Code, response.Body.String())
		}
	}
}

func waitPackageHTTPTest(t *testing.T, s *dms.Service, id string) *dms.OperationView {
	t.Helper()
	for i := 0; i < 250; i++ {
		op, err := s.Operation(t.Context(), "organization-admin", id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status != "pending" {
			if op.Status != "succeeded" {
				t.Fatal("package operation", op.Status, op.Error)
			}
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("package operation did not complete")
	return nil
}
