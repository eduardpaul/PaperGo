package httpapi

import (
	"mime"
	"net/http"
	"strconv"
	"time"

	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/tasks"
	"papergo/internal/transfer"
)

func (a *API) registerTasks(api *router) {
	api.HandleFunc("GET /v1/me/tasks", a.myTasks)
	api.HandleFunc("GET /v1/workspaces/{id}/tasks", a.workspaceTasks)
	api.HandleFunc("POST /v1/workspaces/{id}/tasks", a.createTask)
	api.HandleFunc("GET /v1/items/{id}/tasks", a.itemTasks)
	api.HandleFunc("GET /v1/tasks/{id}", a.getTask)
	api.HandleFunc("POST /v1/tasks/{id}/responses", a.respondTask)
	api.HandleFunc("POST /v1/tasks/{id}/reassign", a.reassignTask)
	api.HandleFunc("POST /v1/tasks/{id}/cancel", a.cancelTask)
	api.HandleFunc("GET /v1/staged-files/{id}", a.getStagedFile)
	api.HandleFunc("GET /v1/staged-files/{id}/content", a.stagedFileContent)
}

func (a *API) tasks() *tasks.Service { return &tasks.Service{DMS: a.DMS} }

func taskFilter(r *http.Request) tasks.Filter {
	q := r.URL.Query()
	f := tasks.Filter{Status: taskstore.Status(q.Get("status")), Kind: taskstore.Kind(q.Get("kind")), Key: q.Get("key"), WorkspaceID: q.Get("workspace_id"), ItemID: q.Get("item_id"), Waiting: q.Get("waiting") == "true", After: q.Get("$skiptoken")}
	if n, err := strconv.Atoi(q.Get("$top")); err == nil {
		f.Limit = n
	}
	return f
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request, out dms.Page[tasks.View], err error) {
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) myTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks().Mine(r.Context(), subject(r), taskFilter(r))
	a.listTasks(w, r, out, err)
}

func (a *API) workspaceTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks().Workspace(r.Context(), subject(r), r.PathValue("id"), taskFilter(r))
	a.listTasks(w, r, out, err)
}

func (a *API) itemTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks().Item(r.Context(), subject(r), r.PathValue("id"), taskFilter(r))
	a.listTasks(w, r, out, err)
}

// taskCreate is a standalone task as POST /v1/workspaces/{id}/tasks takes it.
type taskCreate struct {
	ItemID      string               `json:"item_id"`
	Kind        taskstore.Kind       `json:"kind"`
	Key         string               `json:"key"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Assignees   []string             `json:"assignees"`
	Completion  taskstore.Completion `json:"completion"`
	Outcomes    []model.TaskOutcome  `json:"outcomes"`
	InputSchema map[string]any       `json:"input_schema"`
	Values      map[string]any       `json:"values"`
	Priority    taskstore.Priority   `json:"priority"`
	DueAt       *time.Time           `json:"due_at"`
	Review      map[string]any       `json:"review"`
}

func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	var in taskCreate
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.tasks().Create(r.Context(), subject(r), runOf(r), tasks.Input{WorkspaceID: r.PathValue("id"), ItemID: in.ItemID, Kind: in.Kind, Key: in.Key, Title: in.Title,
		Description: in.Description, Assignees: in.Assignees, Completion: in.Completion, Outcomes: in.Outcomes, InputSchema: in.InputSchema, Values: in.Values,
		Priority: in.Priority, DueAt: in.DueAt, Review: in.Review, Actor: subject(r)})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/tasks/"+out.ID)
	respond(w, r, 201, out)
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks().Get(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) respondTask(w http.ResponseWriter, r *http.Request) {
	var in tasks.Response
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.tasks().Respond(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) reassignTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Assignees []string `json:"assignees"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.tasks().Reassign(r.Context(), subject(r), r.PathValue("id"), in.Assignees)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks().Cancel(r.Context(), subject(r), r.PathValue("id"))
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
	fresh, err := a.DMS.StagedFresh(r.Context(), f.ID)
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
