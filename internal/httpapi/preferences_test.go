package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	directorydomain "papergo/internal/dal/directory"
	"sync"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/identity"
)

func TestPreferencesInheritanceAndIsolationAPI(t *testing.T) {
	h, s, _ := setup(t)
	directory := s.Directory()
	role, err := directory.SaveRole(context.Background(), "organization-admin", "", 0, identity.RoleInput{Name: "Preferences administrator", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(context.Background(), "organization-admin", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	mine := "/v1/identity/me/preferences"
	org := "/v1/organization/preferences"
	read := func(path string) identity.Preferences {
		t.Helper()
		res := request(h, "GET", path, "", testToken, "", "")
		var out identity.Preferences
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		return out
	}
	for _, code := range []string{"eur", "EURO", "E1R"} {
		response := request(h, "PATCH", org, `{"default_currency":"`+code+`"}`, testToken, `"0"`, "application/json")
		if response.Code != 422 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	initial := read(mine)
	if initial.DefaultCurrency != "USD" || initial.Language != "en" || initial.TimeZone != "UTC" || initial.Theme != "system" || initial.DocumentLanguages != "eng" || len(initial.Inherited) != 8 || initial.Version != 0 {
		t.Fatal(initial)
	}
	res := request(h, "PATCH", org, `{"default_currency":"EUR","language":" de-DE ","time_zone":"Europe/Berlin","theme":"dark","document_languages":"deu+eng"}`, testToken, `"0"`, "application/merge-patch+json")
	if res.Code != 200 || res.Header().Get("ETag") != `"1"` {
		t.Fatal(res.Code, res.Body.String())
	}
	out := read(mine)
	if out.DefaultCurrency != "EUR" || out.Language != "de-DE" || out.TimeZone != "Europe/Berlin" || out.DocumentLanguages != "deu+eng" || out.Version != 0 || len(out.Inherited) != 8 {
		t.Fatal(out)
	}
	res = request(h, "PATCH", mine, `{"language":"fr-FR","theme":"light"}`, testToken, `"0"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", org, `{"language":"es","theme":"system","time_format":"12h"}`, testToken, `"1"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	out = read(mine)
	if out.Language != "fr-FR" || out.Theme != "light" || out.TimeFormat != "12h" || len(out.Inherited) != 6 || out.Version != 1 {
		t.Fatal(out)
	}
	res = request(h, "PATCH", mine, `{"language":null}`, testToken, `"1"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	out = read(mine)
	if out.Language != "es" || out.Theme != "light" || len(out.Inherited) != 7 {
		t.Fatal(out)
	}
	res = request(h, "PATCH", org, `{"language":null}`, testToken, `"2"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	out = read(mine)
	if out.Language != "en" || out.Version != 2 {
		t.Fatal(out)
	}
	// Organization reads require a signed-in active user, while modifying the
	// defaults requires organization.manage independently of personal editing.
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	res = request(bob, "GET", mine, "", testToken, "", "")
	var prefs identity.Preferences
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &prefs) != nil || prefs.Theme != "system" || prefs.Version != 0 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(bob, "PATCH", org, `{"theme":"dark"}`, testToken, `"3"`, "application/json")
	if res.Code != 403 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(bob, "PATCH", mine, `{"theme":"dark"}`, testToken, `"0"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	if _, err := s.Directory().SetUserActive(t.Context(), "alice", "bob", false); err != nil {
		t.Fatal(err)
	}
	res = request(bob, "GET", mine, "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal(res.Code, res.Body.String())
	}
}

func TestPreferencesValidationAndConcurrentUpdatesAPI(t *testing.T) {
	h, _, _ := setup(t)
	path := "/v1/identity/me/preferences"
	for _, test := range []struct {
		body, match string
		status      int
	}{
		{`{}`, "", 428}, {`{}`, `"-1"`, 400}, {`{}`, `"1"`, 412},
		{`{"default_currency":"EUR"}`, `"0"`, 422}, {`{"unknown":null}`, `"0"`, 422}, {`{"theme":"blue"}`, `"0"`, 422},
		{`{"time_zone":"Invalid/Zone"}`, `"0"`, 422}, {`{"time_zone":"Local"}`, `"0"`, 422},
		{`{"date_format":"dd/yy"}`, `"0"`, 422}, {`{"document_languages":"ENG"}`, `"0"`, 422},
		{`{"language":"not-a-culture"}`, `"0"`, 422}, {`{"number_format":""}`, `"0"`, 422},
		{`{"time_format":"25h"}`, `"0"`, 422}, {`null`, `"0"`, 422},
		{`{"theme":false}`, `"0"`, 400},
	} {
		res := request(h, "PATCH", path, test.body, testToken, test.match, "application/json")
		if res.Code != test.status {
			t.Fatal(test, res.Code, res.Body.String())
		}
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, theme := range []string{"light", "dark"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := request(h, "PATCH", path, `{"theme":"`+theme+`"}`, testToken, `"0"`, "application/json")
			statuses <- res.Code
		}()
	}
	wg.Wait()
	close(statuses)
	ok, conflict := 0, 0
	for status := range statuses {
		if status == 200 {
			ok++
		} else if status == 412 {
			conflict++
		} else {
			t.Fatal(status)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("concurrent preference overwrite", ok, conflict)
	}
}
