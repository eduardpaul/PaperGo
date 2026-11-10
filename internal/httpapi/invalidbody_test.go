package httpapi

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
)

// A body the API cannot read says which property or value is wrong.
func TestInvalidBodySaysWhatIsWrong(t *testing.T) {
	s := dms.NewService(daltest.Database(t).SQL)
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler()
	for body, want := range map[string]string{
		`{"name": "Casa", "colour": "red"}`: `invalid JSON request body: unknown property \"colour\"`,
		`{"name": 7}`:                       `invalid JSON request body: name takes a string, not a number`,
		`{"name": "Casa"`:                   `invalid JSON request body: it ends early or is empty`,
		`{"name" "Casa"}`:                   `invalid JSON request body: it is not JSON at byte`,
	} {
		r := httptest.NewRequest("POST", "/v1/workspaces", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s, want %s", body, w.Code, w.Body, want)
		}
	}
}
