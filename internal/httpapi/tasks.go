package httpapi

import (
	"encoding/json"
	"errors"
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
	api.HandleFunc("PATCH /v1/tasks/{id}", a.updateTask)
	api.HandleFunc("POST /v1/tasks/{id}/responses", a.respondTask)
	api.HandleFunc("POST /v1/tasks/{id}/reassign", a.reassignTask)
	api.HandleFunc("POST /v1/tasks/{id}/cancel", a.cancelTask)
	api.HandleFunc("GET /v1/staged-files/{id}", a.getStagedFile)
	api.HandleFunc("GET /v1/staged-files/{id}/content", a.stagedFileContent)
}

func (a *API) tasks(r *http.Request) *tasks.Service { return &tasks.Service{DMS: a.dms(r)} }

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
	out, err := a.tasks(r).Mine(r.Context(), subject(r), taskFilter(r))
	a.listTasks(w, r, out, err)
}

func (a *API) workspaceTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks(r).Workspace(r.Context(), subject(r), r.PathValue("id"), taskFilter(r))
	a.listTasks(w, r, out, err)
}

func (a *API) itemTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.tasks(r).Item(r.Context(), subject(r), r.PathValue("id"), taskFilter(r))
	a.listTasks(w, r, out, err)
}

// taskCreate is a standalone task as POST /v1/workspaces/{id}/tasks takes it.
type taskCreate struct {
	ItemID      string               `json:"item_id"`
	Kind        taskstore.Kind       `json:"kind"`
	Key         string               `json:"key"`
	Unique      bool                 `json:"unique"`
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
	out, err := a.tasks(r).Create(r.Context(), subject(r), runOf(r), tasks.Input{WorkspaceID: r.PathValue("id"), ItemID: in.ItemID, Kind: in.Kind, Key: in.Key, Unique: in.Unique, Title: in.Title,
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
	out, err := a.tasks(r).Get(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
}

// updateTask changes an open task's title, description, priority, due date
// or form prefill at the version If-Match names. null clears due_at and
// values.
func (a *API) updateTask(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in map[string]json.RawMessage
	if !a.decode(w, r, &in) {
		return
	}
	var p tasks.Patch
	for key, raw := range in {
		null := string(raw) == "null"
		var err error
		switch key {
		case "title":
			err = json.Unmarshal(raw, &p.Title)
		case "description":
			p.Description = new(string)
			if !null {
				err = json.Unmarshal(raw, p.Description)
			}
		case "priority":
			err = json.Unmarshal(raw, &p.Priority)
		case "due_at":
			p.SetDueAt = true
			err = json.Unmarshal(raw, &p.DueAt)
		case "values":
			p.SetValues = true
			err = json.Unmarshal(raw, &p.Values)
		default:
			err = errors.New("a task update has no field " + key)
		}
		if err == nil && null && (key == "title" || key == "priority") {
			err = errors.New(key + " cannot be null")
		}
		if err != nil {
			a.failure(w, r, dms.Invalid(err.Error()))
			return
		}
	}
	out, err := a.tasks(r).Update(r.Context(), subject(r), r.PathValue("id"), int64(version), p)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
}

func (a *API) respondTask(w http.ResponseWriter, r *http.Request) {
	var in tasks.Response
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.tasks(r).Respond(r.Context(), subject(r), r.PathValue("id"), in)
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
	out, err := a.tasks(r).Reassign(r.Context(), subject(r), r.PathValue("id"), in.Assignees)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	// The body, with the reason, is optional.
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !a.decode(w, r, &in) {
		return
	}
	out, err := a.tasks(r).Cancel(r.Context(), subject(r), r.PathValue("id"), in.Reason)
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
	ArtifactID  string    `json:"artifact_id"`
	RunID       string    `json:"run_id,omitempty"`
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
	f, err := a.dms(r).StagedFile(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	fresh, err := a.dms(r).StagedFresh(r.Context(), f.ID)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	art := f.Artifact
	respond(w, r, 200, stagedFile{ID: f.ID, ItemID: f.ItemID, ArtifactID: art.ID, RunID: f.RunID, Filename: art.Filename, ContentType: art.ContentType, Size: art.Size,
		SHA256: art.Sha256, Metrics: art.Metrics, Sources: art.Sources, State: string(f.State), Fresh: fresh, CreatedAt: f.CreatedAt})
}

// stagedFileContent serves a staged file's bytes inline, for review screens.
func (a *API) stagedFileContent(w http.ResponseWriter, r *http.Request) {
	f, err := a.dms(r).StagedFile(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	art := f.Artifact
	file, err := a.Storage.Open(r.Context(), art.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", art.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": art.Filename}))
	transfer.StoredFile(w)
	w.Header().Set("ETag", strconv.Quote(art.Sha256))
	http.ServeContent(transfer.Writer(w), r, art.Filename, art.CreatedAt, file)
}
