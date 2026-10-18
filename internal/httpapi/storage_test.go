package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestStorageStatusRequiresOrganizationManage(t *testing.T) {
	h, s, _ := setup(t)
	if res := request(h, "GET", "/v1/storage/status", "", "", "", ""); res.Code != 401 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res := request(h, "GET", "/v1/storage/status", "", testToken, "", ""); res.Code != 403 {
		t.Fatal(res.Code, res.Body.String())
	}
	ctx := context.Background()
	role, err := s.Directory().SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Storage administrator", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	res := request(h, "GET", "/v1/storage/status", "", testToken, "", "")
	var got dms.StorageStatus
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &got) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	pages := int64(got.PageCount) * int64(got.PageSize)
	if got.JournalMode != "wal" || got.Synchronous != "FULL" || got.AutoCheckpoint != database.WALAutoCheckpointPages || got.JournalLimit != database.JournalSizeLimitBytes || got.DatabaseBytes != pages || got.AccountedBytes+got.FreelistBytes != pages || got.Tables == nil || got.Runs == nil || got.SearchStore != "sqlite" {
		t.Fatalf("%+v", got)
	}
	direct, err := database.ObserveCheckpoint(ctx, s.Directory().DB)
	if err != nil {
		t.Fatal(err)
	}
	if direct.JournalMode != got.JournalMode || direct.Synchronous != got.Synchronous || direct.AutoCheckpoint != got.AutoCheckpoint || direct.JournalLimit != got.JournalLimit {
		t.Fatalf("direct %+v http %+v", direct, got)
	}
}
