package apps

import (
	"net/http"
	"strconv"

	"papergo/internal/httpapi"
)

// Module serves the apps routes.
func Module() httpapi.Module { return module{} }

type module struct{}

func (module) Name() string { return "apps" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/apps", OperationID: "listApps", Handle: m.list},
		{Method: http.MethodPost, Pattern: "/v1/workspaces/{id}/apps", OperationID: "createApp", Handle: m.create},
		{Method: http.MethodGet, Pattern: "/v1/apps", OperationID: "listLauncherApps", Handle: m.launcher},
		{Method: http.MethodGet, Pattern: "/v1/apps/{id}", OperationID: "getApp", Handle: m.get},
		{Method: http.MethodPut, Pattern: "/v1/apps/{id}", OperationID: "updateApp", Handle: m.update},
		{Method: http.MethodDelete, Pattern: "/v1/apps/{id}", OperationID: "deleteApp", Handle: m.delete},
		{Method: http.MethodGet, Pattern: "/v1/apps/{id}/versions", OperationID: "listAppVersions", Handle: m.versions},
		{Method: http.MethodGet, Pattern: "/v1/apps/{id}/bundle", OperationID: "getAppBundle", Handle: m.bundle},
	}
}

func service(in httpapi.Request) *Service { return &Service{DMS: in.DMS} }

func (module) list(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).List(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

// launcher lists the apps the caller may see in every workspace.
func (module) launcher(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Launcher(r.Context(), in.Subject)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (module) create(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body Save
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := service(in).Create(r.Context(), in.Subject, r.PathValue("id"), body)
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/v1/apps/"+out.ID)
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusCreated, out)
	return nil
}

func (module) get(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Get(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) update(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	var body Save
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := service(in).Update(r.Context(), in.Subject, r.PathValue("id"), version, body)
	if err != nil {
		return err
	}
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) delete(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	if err := service(in).Delete(r.Context(), in.Subject, r.PathValue("id"), version); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (module) versions(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Versions(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

// bundle serves the compiled code of the app's current version. The app page
// hands it to the app's frame; the code is not run in PaperGo's own page.
func (module) bundle(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Bundle(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", strconv.Quote(out.Hash))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("PaperGo-App-Version", strconv.Itoa(out.Number))
	if r.Header.Get("If-None-Match") == strconv.Quote(out.Hash) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	_, err = w.Write([]byte(out.Code))
	return err
}
