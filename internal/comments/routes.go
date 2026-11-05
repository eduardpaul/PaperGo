package comments

import (
	"encoding/json"
	"net/http"
	"strconv"

	"papergo/internal/dal/live"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// Module serves discussions: their routes, and their frames on the live
// stream.
func Module() httpapi.Module { return module{} }

type module struct{}

func (module) Name() string { return "comments" }

// Feature tells the web app to show discussions.
func (module) Feature() string { return "comments" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodGet, Pattern: "/v1/items/{id}/comments", OperationID: "listItemComments", Handle: m.itemComments},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/comments", OperationID: "createComment", Handle: m.createComment},
		{Method: http.MethodGet, Pattern: "/v1/items/{id}/mentionable", OperationID: "listMentionable", Handle: m.mentionable},
		{Method: http.MethodGet, Pattern: "/v1/comments/{id}", OperationID: "getComment", Handle: m.getComment},
		{Method: http.MethodPatch, Pattern: "/v1/comments/{id}", OperationID: "editComment", Handle: m.editComment},
		{Method: http.MethodDelete, Pattern: "/v1/comments/{id}", OperationID: "deleteComment", Handle: m.deleteComment},
		{Method: http.MethodPost, Pattern: "/v1/comments/{id}/resolve", OperationID: "resolveCommentThread", Handle: m.resolveComment(true)},
		{Method: http.MethodPost, Pattern: "/v1/comments/{id}/reopen", OperationID: "reopenCommentThread", Handle: m.resolveComment(false)},
		{Method: http.MethodPost, Pattern: "/v1/comments/counts", OperationID: "countCommentThreads", Handle: m.commentCounts},
		{Method: http.MethodGet, Pattern: "/v1/resources/{id}/settings/comments", OperationID: "getCommentSettings", Handle: m.commentSettings},
		{Method: http.MethodPut, Pattern: "/v1/resources/{id}/settings/comments", OperationID: "setCommentSettings", Handle: m.setCommentSettings},
		{Method: http.MethodGet, Pattern: "/v1/organization/settings/comments", OperationID: "getOrganizationCommentSettings", Handle: m.organizationCommentSettings},
		{Method: http.MethodPut, Pattern: "/v1/organization/settings/comments", OperationID: "setOrganizationCommentSettings", Handle: m.setOrganizationCommentSettings},
	}
}

// LiveKind is the changes comments make on the live stream.
func (module) LiveKind() live.Kind { return live.KindComment }

// LiveBatch names what changed for people who may read its discussion;
// clients read the thread again. One check of each item serves the batch.
func (module) LiveBatch(r *http.Request, in httpapi.Request) func(live.Change) (*httpapi.LiveFrame, error) {
	service := &Service{DMS: in.DMS}
	readable := map[string]bool{}
	return func(change live.Change) (*httpapi.LiveFrame, error) {
		c, visible, err := service.Visible(r.Context(), in.Subject, change.EntityID, readable)
		if err != nil || !visible {
			return nil, err
		}
		return &httpapi.LiveFrame{Event: "comment", Data: map[string]any{"id": c.ID, "item_id": c.ItemID, "thread_id": c.ThreadID, "action": change.Action}}, nil
	}
}

func (module) itemComments(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("$top"))
	out, err := (&Service{DMS: in.DMS}).Threads(r.Context(), in.Subject, r.PathValue("id"), Query{Status: Status(q.Get("status")), AnchorKind: q.Get("anchor_kind"), Limit: limit, After: q.Get("$skiptoken")})
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) createComment(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body struct {
		Body     string          `json:"body"`
		ThreadID string          `json:"thread_id"`
		Anchor   json.RawMessage `json:"anchor"`
	}
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := (&Service{DMS: in.DMS}).Create(r.Context(), in.Subject, in.Run, r.PathValue("id"), Input{Body: body.Body, ThreadID: body.ThreadID, Anchor: body.Anchor})
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/v1/comments/"+out.ID)
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusCreated, out)
	return nil
}

func (module) getComment(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := (&Service{DMS: in.DMS}).Get(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) editComment(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	var body struct {
		Body string `json:"body"`
	}
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := (&Service{DMS: in.DMS}).Edit(r.Context(), in.Subject, r.PathValue("id"), int64(version), body.Body)
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) deleteComment(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	if err := (&Service{DMS: in.DMS}).Delete(r.Context(), in.Subject, r.PathValue("id"), int64(version)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (module) resolveComment(resolved bool) func(http.ResponseWriter, *http.Request, httpapi.Request) error {
	return func(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
		version, ok := in.IfMatch(w, r)
		if !ok {
			return nil
		}
		out, err := (&Service{DMS: in.DMS}).Resolve(r.Context(), in.Subject, r.PathValue("id"), int64(version), resolved)
		if err != nil {
			return err
		}
		httpapi.ETag(w, int(out.Version))
		httpapi.Respond(w, r, http.StatusOK, out)
		return nil
	}
}

func (module) commentCounts(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := (&Service{DMS: in.DMS}).Counts(r.Context(), in.Subject, body.ItemIDs)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"counts": out})
	return nil
}

func (module) mentionable(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := (&Service{DMS: in.DMS}).Mentionable(r.Context(), in.Subject, r.PathValue("id"), r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (module) commentSettings(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := (&Service{DMS: in.DMS}).PlaceSettings(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) setCommentSettings(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatchOrNew(w, r)
	if !ok {
		return nil
	}
	var body struct {
		Mode *Mode `json:"mode"`
	}
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := (&Service{DMS: in.DMS}).SetPlace(r.Context(), in.Subject, r.PathValue("id"), int64(version), body.Mode)
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) organizationCommentSettings(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := (&Service{DMS: in.DMS}).Organization(r.Context())
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (module) setOrganizationCommentSettings(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatchOrNew(w, r)
	if !ok {
		return nil
	}
	var body struct {
		Enabled *bool `json:"enabled"`
		Mode    *Mode `json:"mode"`
	}
	if !in.Decode(w, r, &body) {
		return nil
	}
	if body.Enabled == nil {
		return dms.Invalid("enabled is required")
	}
	out, err := (&Service{DMS: in.DMS}).SetOrganization(r.Context(), in.Subject, int64(version), *body.Enabled, body.Mode)
	if err != nil {
		return err
	}
	httpapi.ETag(w, int(out.Version))
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}
