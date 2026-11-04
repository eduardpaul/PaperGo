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
	httpapi.SaveSampleApp = func(t *testing.T, s *dms.Service, workspace, id string, version int, app httpapi.SampleApp) {
		t.Helper()
		service := &apps.Service{DMS: s}
		save := apps.Save{Key: app.Key, Name: app.Name, Description: app.Description, Source: app.Source, Parameters: app.Parameters, State: app.State}
		var err error
		if id == "" {
			_, err = service.Create(t.Context(), "organization-admin", workspace, save)
		} else {
			save.Key = ""
			_, err = service.Update(t.Context(), "organization-admin", id, version, save)
		}
		if err != nil {
			t.Fatal(app.Name, err)
		}
	}
}
