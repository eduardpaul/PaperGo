package httpapi

import (
	"mime"
	"net/http"
	"strconv"
	"time"

	"papergo/internal/transfer"
	"papergo/internal/workflow"
)

func (a *API) registerApprovals(api *http.ServeMux) {
	api.HandleFunc("GET /v1/me/approvals", a.myApprovals)
	api.HandleFunc("GET /v1/workspaces/{id}/approvals", a.workspaceApprovals)
	api.HandleFunc("GET /v1/approvals/{id}", a.getApproval)
	api.HandleFunc("POST /v1/approvals/{id}/decision", a.decideApproval)
	api.HandleFunc("GET /v1/staged-files/{id}", a.getStagedFile)
	api.HandleFunc("GET /v1/staged-files/{id}/content", a.stagedFileContent)
}

func approvalFilter(r *http.Request) workflow.ApprovalFilter {
	f := workflow.ApprovalFilter{Status: r.URL.Query().Get("status")}
	if n, err := strconv.Atoi(r.URL.Query().Get("$top")); err == nil {
		f.Limit = n
	}
	return f
}

func (a *API) myApprovals(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.MyApprovals(r.Context(), subject(r), approvalFilter(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
}

func (a *API) workspaceApprovals(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.WorkspaceApprovals(r.Context(), subject(r), r.PathValue("id"), approvalFilter(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
}

func (a *API) getApproval(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.Approval(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) decideApproval(w http.ResponseWriter, r *http.Request) {
	var in workflow.Decision
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.Decide(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

// stagedFile is a staged file as the API shows it.
type stagedFile struct {
	ID          string    `json:"id"`
	ItemID      string    `json:"item_id"`
	RunID       string    `json:"run_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	Metrics     any       `json:"metrics"`
	Sources     any       `json:"sources"`
	State       string    `json:"state"`
	Fresh       bool      `json:"fresh"`
	CreatedAt   time.Time `json:"created_at"`
}

func (a *API) getStagedFile(w http.ResponseWriter, r *http.Request) {
	f, err := a.DMS.StagedFile(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	fresh, err := a.DMS.StagedFresh(r.Context(), f)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, stagedFile{ID: f.ID, ItemID: f.ItemID, RunID: f.RunID, Filename: f.Filename, ContentType: f.ContentType, Size: f.Size,
		SHA256: f.Sha256, Metrics: f.Metrics, Sources: f.Sources, State: string(f.State), Fresh: fresh, CreatedAt: f.CreatedAt})
}

// stagedFileContent serves a staged file's bytes inline, for review screens.
func (a *API) stagedFileContent(w http.ResponseWriter, r *http.Request) {
	f, err := a.DMS.StagedFile(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	file, err := a.Storage.Open(r.Context(), f.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": f.Filename}))
	w.Header().Set("ETag", strconv.Quote(f.Sha256))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(transfer.Writer(w), r, f.Filename, f.CreatedAt, file)
}
