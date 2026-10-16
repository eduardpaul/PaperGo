package httpapi

import (
	"net/http"
	"os"
	"papergo/internal/dms"
	"papergo/internal/transfer"
	"strconv"
	"strings"
	"time"
)

func (a *API) registerPortability(m *http.ServeMux) {
	m.HandleFunc("POST /v1/portability/exports", func(w http.ResponseWriter, r *http.Request) {
		if a.Storage == nil {
			a.problem(w, r, 503, "storage_unavailable", "package storage is unavailable")
			return
		}
		var options dms.ExportOptions
		if r.ContentLength != 0 {
			if !a.decode(w, r, &options) {
				return
			}
		}
		op, err := a.DMS.StartExport(r.Context(), subject(r), options)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		w.Header().Set("Location", "/v1/operations/"+op.ID)
		respond(w, r, 202, op)
	})
	m.HandleFunc("POST /v1/portability/imports", func(w http.ResponseWriter, r *http.Request) {
		if err := a.DMS.RequireScope(r.Context(), subject(r), "organization.manage"); err != nil {
			a.failure(w, r, err)
			return
		}
		if a.Storage == nil {
			a.problem(w, r, 503, "storage_unavailable", "package storage is unavailable")
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/zip" {
			a.problem(w, r, 415, "unsupported_media_type", "send a PaperGo package as application/zip")
			return
		}
		dryRun := false
		if value := r.URL.Query().Get("dry_run"); value != "" {
			var err error
			dryRun, err = strconv.ParseBool(value)
			if err != nil {
				a.problem(w, r, 400, "invalid_dry_run", "dry_run must be a boolean")
				return
			}
		}
		object, err := a.Storage.Put(r.Context(), transfer.Reader(r.Body, w), dms.PackageLimit)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		op, err := a.DMS.StartImport(r.Context(), subject(r), object.Key, dryRun, r.URL.Query().Get("workspace_name"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		w.Header().Set("Location", "/v1/operations/"+op.ID)
		respond(w, r, 202, op)
	})
	m.HandleFunc("GET /v1/portability/exports/{id}/package", func(w http.ResponseWriter, r *http.Request) {
		if a.Storage == nil {
			a.problem(w, r, 503, "storage_unavailable", "package storage is unavailable")
			return
		}
		result, err := a.DMS.ExportPackage(r.Context(), subject(r), r.PathValue("id"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		file, err := a.Storage.Open(r.Context(), result.ObjectKey)
		if os.IsNotExist(err) {
			err = dms.ErrNotFound
		}
		if err != nil {
			a.failure(w, r, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="papergo-core.zip"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("ETag", `"`+result.SHA256+`"`)
		http.ServeContent(transfer.Writer(w), r, "papergo-core.zip", time.Time{}, file)
	})
}
