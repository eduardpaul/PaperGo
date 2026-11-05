package httpapi_test

import (
	"testing"

	"papergo/internal/apps"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// The apps module imports httpapi, so the sample package test saves its
// apps through this file, outside the package.
func init() {
	httpapi.SaveSampleApp = func(t *testing.T, s *dms.Service, workspace, id string, version int, app httpapi.SampleApp) map[string]any {
		t.Helper()
		service := &apps.Service{DMS: s}
		save := apps.Save{Key: app.Key, Name: app.Name, Description: app.Description, Source: app.Source, Parameters: app.Parameters, State: app.State}
		var saved apps.View
		var err error
		if id == "" {
			saved, err = service.Create(t.Context(), "organization-admin", workspace, save)
		} else {
			save.Key = ""
			saved, err = service.Update(t.Context(), "organization-admin", id, version, save)
		}
		if err != nil {
			t.Fatal(app.Name, err)
		}
		return saved.Parameters
	}
}
