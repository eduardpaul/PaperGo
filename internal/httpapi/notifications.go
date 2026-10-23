package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"papergo/internal/dal/notifications"
	"papergo/internal/notify"
)

func (a *API) registerNotifications(api *router) {
	api.HandleFunc("GET /v1/me/notifications", a.myNotifications)
	api.HandleFunc("GET /v1/me/notifications/unread-count", a.unreadNotifications)
	api.HandleFunc("POST /v1/me/notifications/read", a.readNotifications)
	api.HandleFunc("DELETE /v1/me/notifications/{id}", a.deleteNotification)
	api.HandleFunc("POST /v1/workspaces/{id}/notifications", a.sendNotification)
}

func (a *API) notifications(r *http.Request) *notify.Service {
	return &notify.Service{DMS: a.dms(r)}
}

func (a *API) myNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := notify.Filter{Unread: q.Get("unread") == "true", Type: notifications.Type(q.Get("type")), WorkspaceID: q.Get("workspace_id"), After: q.Get("$skiptoken")}
	if n, err := strconv.Atoi(q.Get("$top")); err == nil {
		f.Limit = n
	}
	out, err := a.notifications(r).Inbox(r.Context(), subject(r), f)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) unreadNotifications(w http.ResponseWriter, r *http.Request) {
	n, err := a.notifications(r).Unread(r.Context(), subject(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	respond(w, r, 200, map[string]int64{"count": n})
}

func (a *API) readNotifications(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []string   `json:"ids"`
		Before *time.Time `json:"before"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	n, err := a.notifications(r).MarkRead(r.Context(), subject(r), in.IDs, in.Before)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]int{"marked": n})
}

func (a *API) deleteNotification(w http.ResponseWriter, r *http.Request) {
	if err := a.notifications(r).Delete(r.Context(), subject(r), r.PathValue("id")); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notificationSend is a message as POST /v1/workspaces/{id}/notifications
// takes it.
type notificationSend struct {
	Recipients []string `json:"recipients"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	ItemID     string   `json:"item_id"`
	DedupKey   string   `json:"dedup_key"`
}

func (a *API) sendNotification(w http.ResponseWriter, r *http.Request) {
	var in notificationSend
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.notifications(r).Send(r.Context(), subject(r), runOf(r), notify.SendInput{WorkspaceID: r.PathValue("id"), Recipients: in.Recipients,
		Title: in.Title, Body: in.Body, ItemID: in.ItemID, DedupKey: in.DedupKey})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
