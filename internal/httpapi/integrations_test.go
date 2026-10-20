package httpapi

import (
	"encoding/json"
	"github.com/google/uuid"
	"papergo/internal/dal/integrations"
	"papergo/internal/dms"
	"papergo/internal/integration"
	"strings"
	"testing"
	"time"
)

func TestIntegrationAPISecrecyPreconditionsAndManagedFields(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	key, err := integration.NewSecrets(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureIntegrations(key)
	workspace, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", workspace.ID, dms.CreateResource{Kind: "list", Name: "List"})
	if err != nil {
		t.Fatal(err)
	}
	privateURL := "https://example.com/do-not-return-this"
	encrypted, err := key.Encrypt(list.ID, `{"url":"`+privateURL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.DAL.Integrations().Create(ctx, integrations.Integration{ContainerID: list.ID, Kind: "ical_https", Name: "Feed", Generation: uuid.NewString(), ConfiguredBy: "alice", ConfigCiphertext: encrypted, ConfigHash: "private-config-hash"})
	if err != nil {
		t.Fatal(err)
	}
	source.State = json.RawMessage(`{"etag":"private-http-validator"}`)
	if err = s.DAL.Integrations().Save(ctx, &source); err != nil {
		t.Fatal(err)
	}
	path := "/v1/resources/" + list.ID + "/integrations"
	response := request(h, "GET", path, "", testToken, "", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, secret := range []string{privateURL, "private-config-hash", "private-http-validator", "ciphertext", "configured_by"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("secret leaked", response.Body.String())
		}
	}
	response = request(h, "PATCH", path+"/"+source.ID, `{"paused":true}`, testToken, "", "application/json")
	if response.Code != 428 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "PATCH", path+"/"+source.ID, `{"paused":true}`, testToken, `"1"`, "application/json")
	if response.Code != 200 || response.Header().Get("ETag") != `"2"` {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "DELETE", path+"/"+source.ID, "", testToken, `"1"`, "")
	if response.Code != 412 {
		t.Fatal(response.Code, response.Body.String())
	}
	leased, err := s.DAL.Integrations().Find(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute)
	leased.LeaseUntil = &until
	if err = s.DAL.Integrations().Save(ctx, &leased); err != nil {
		t.Fatal(err)
	}
	response = request(h, "DELETE", path+"/"+source.ID, "", testToken, `"2"`, "")
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Managed title"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DAL.Integrations().AddItem(ctx, integrations.Item{ItemID: item.ID, IntegrationID: source.ID, ProviderKind: "ical_https", ExternalID: "external", ManagedFields: []string{"$name"}}); err != nil {
		t.Fatal(err)
	}
	response = request(h, "GET", "/v1/items/"+item.ID+"/integration", "", testToken, "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "$name") {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "PATCH", "/v1/resources/"+item.ID, `{"name":"override"}`, testToken, `"1"`, "application/json")
	if response.Code != 422 {
		t.Fatal(response.Code, response.Body.String())
	}
	// IP literals do not require external DNS; private destinations fail before saving.
	response = request(h, "POST", path, `{"kind":"ical_https","name":"Private","configuration":{"url":"https://127.0.0.1/feed"}}`, testToken, "", "application/json")
	if response.Code != 422 {
		t.Fatal(response.Code, response.Body.String())
	}
}
