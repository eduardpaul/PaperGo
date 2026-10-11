package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/storage"
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
	var op ent.Operation
	if err = json.Unmarshal(response.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.RunOperations(ctx, a.Logger) }()
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
