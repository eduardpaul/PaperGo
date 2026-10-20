package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

func TestMain(m *testing.M) { daltest.Main(m) }

// The API this executable composes, its feature modules included, serves
// exactly the operations api/openapi.json describes.
func TestRoutesMatchOpenAPI(t *testing.T) {
	db := daltest.Database(t)
	a := &httpapi.API{DMS: dms.NewService(db.SQL), Auth: auth.Local{DAL: db.DAL}, Logger: slog.Default(), Modules: testFeatures().modules()}
	served, err := a.Routes()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var described []string
	for path, item := range document.Paths {
		for method := range item {
			if method != "parameters" {
				described = append(described, strings.ToUpper(method)+" "+path)
			}
		}
	}
	var missing, extra []string
	for _, route := range served {
		if !slices.Contains(described, route) {
			missing = append(missing, route)
		}
	}
	for _, route := range described {
		if !slices.Contains(served, route) {
			extra = append(extra, route)
		}
	}
	slices.Sort(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("routes without a contract: %v\ncontract without a route: %v", missing, extra)
	}
}
