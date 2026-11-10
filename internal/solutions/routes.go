package solutions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"papergo/internal/buildinfo"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// MaxPackageBytes bounds a packed solution sent to the API.
const MaxPackageBytes = 16 << 20

// Module serves the solutions routes.
func Module() httpapi.Module { return module{} }

type module struct{}

func (module) Name() string { return "solutions" }

// Feature names solutions in /auth/config while the deployment serves them.
func (module) Feature() string { return "solutions" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodPost, Pattern: "/v1/workspaces/{id}/solutions/plan", OperationID: "planSolution", Handle: m.plan},
		{Method: http.MethodPost, Pattern: "/v1/workspaces/{id}/solutions/apply", OperationID: "applySolution", Handle: m.apply},
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/solutions", OperationID: "listSolutions", Handle: m.list},
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/solutions/{key}", OperationID: "getSolution", Handle: m.get},
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/solutions/{key}/status", OperationID: "getSolutionStatus", Handle: m.status},
	}
}

func service(in httpapi.Request) *Service { return &Service{DMS: in.DMS, PaperGo: buildinfo.Version} }

// read reads the packed solution of a request's body and the options of
// its options query parameter.
func read(w http.ResponseWriter, r *http.Request) (Solution, Options, error) {
	var o Options
	if raw := r.URL.Query().Get("options"); raw != "" {
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&o); err != nil {
			return Solution{}, o, dms.Invalid("options: " + err.Error())
		}
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPackageBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return Solution{}, o, dms.Invalid("a packed solution is limited to 16 MiB")
		}
		return Solution{}, o, err
	}
	files, err := OpenZIP(raw)
	if err != nil {
		return Solution{}, o, dms.Invalid("the request's body must be a packed solution: " + err.Error())
	}
	s, problems := Load(files)
	if len(problems) > 0 {
		return Solution{}, o, dms.Invalid("the solution has problems:\n" + problems.Error())
	}
	return s, o, nil
}

func (module) plan(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	s, o, err := read(w, r)
	if err != nil {
		return err
	}
	out, err := service(in).Plan(r.Context(), in.Subject, r.PathValue("id"), s, o)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// apply answers 200 with what was applied, and 409 with the refusals when
// nothing was.
func (module) apply(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	s, o, err := read(w, r)
	if err != nil {
		return err
	}
	out, err := service(in).Apply(context.WithoutCancel(r.Context()), in.Subject, r.PathValue("id"), s, o)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if !out.Applied {
		status = http.StatusConflict
	}
	httpapi.Respond(w, r, status, out)
	return nil
}

func (module) list(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).List(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (module) get(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Get(r.Context(), in.Subject, r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) status(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := service(in).Status(r.Context(), in.Subject, r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}
