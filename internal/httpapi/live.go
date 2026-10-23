package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"papergo/internal/dal"
	"papergo/internal/dal/live"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/notify"
	"strconv"
	"strings"
	"sync"
	"time"
)

var livePollInterval = time.Second

const liveKeepAlive = 15 * time.Second

// Streams have their own admission pool, so idle connections do not occupy
// ordinary request slots. A user can hold four of the process's 64 streams.
func (a *API) streamLimiter(next http.Handler) http.Handler {
	var mu sync.Mutex
	active := map[string]int{}
	total := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := subject(r)
		mu.Lock()
		allowed := total < 64 && active[user] < 4
		if allowed {
			total++
			active[user]++
		}
		mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "1")
			a.problem(w, r, 503, "stream_capacity", "live stream capacity is in use")
			return
		}
		defer func() {
			mu.Lock()
			total--
			active[user]--
			if active[user] == 0 {
				delete(active, user)
			}
			mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	var after *int64
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		position, err := strconv.ParseInt(value, 10, 64)
		if err != nil || position < 0 {
			a.problem(w, r, 400, "invalid_live_cursor", "Last-Event-ID must be a non-negative position")
			return
		}
		after = &position
	}
	position, err := a.dms(r).LivePosition(r.Context(), after)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	frame := func(kind string, id int64, value any) error {
		_ = controller.SetWriteDeadline(time.Now().Add(liveKeepAlive))
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, kind, raw); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err = frame("connected", position, map[string]any{"resynchronize": true}); err != nil {
		return
	}
	ticker := time.NewTicker(livePollInterval)
	defer ticker.Stop()
	keepalive := time.NewTicker(liveKeepAlive / 2)
	defer keepalive.Stop()
	end := time.NewTimer(30 * time.Minute)
	defer end.Stop()
	known := map[string]bool{}
	mine := notifications.RecipientKey(subject(r))
	notes := map[string]bool{}
	_, token := identity.AccessTokenContext(r.Context())
	for {
		if err = identity.RequireCredential(r.Context(), dal.NewStore(a.dms(r).Directory().DB), subject(r)); err != nil {
			_ = frame("reauthenticate", position, map[string]any{})
			return
		}
		changes, e := a.dms(r).LiveChanges(r.Context(), position)
		if e != nil {
			return
		}
		last := map[string]int{}
		key := func(changeKind live.Kind, id string) string {
			if changeKind == live.KindAccess {
				return "access"
			}
			return string(changeKind) + ":" + id
		}
		for i, change := range changes {
			last[key(change.Kind, change.EntityID)] = i
		}
		// One unread count serves a batch's notification frames: each would
		// read the same inbox.
		unread := int64(-1)
		count := func() (int64, error) {
			if unread < 0 {
				n, err := a.notifications(r).Unread(r.Context(), subject(r))
				if err != nil {
					return 0, err
				}
				unread = n
			}
			return unread, nil
		}
		for i, change := range changes {
			if err = identity.RequireCredential(r.Context(), dal.NewStore(a.dms(r).Directory().DB), subject(r)); err != nil {
				_ = frame("reauthenticate", position, map[string]any{})
				return
			}
			position = change.ID
			if last[key(change.Kind, change.EntityID)] != i {
				continue
			}
			switch change.Kind {
			case live.KindAccess:
				if err = frame("resync", position, map[string]any{}); err != nil {
					return
				}
			case live.KindItem:
				item, e := a.dms(r).Get(r.Context(), subject(r), change.EntityID)
				if e == nil {
					known[item.ID] = true
					if err = frame("item", position, map[string]any{"id": item.ID, "action": change.Action, "item": item}); err != nil {
						return
					}
				} else if errors.Is(e, dms.ErrForbidden) || errors.Is(e, dms.ErrNotFound) {
					if known[change.EntityID] {
						reason := "changed"
						if change.Action == live.ActionDeleted {
							reason = "deleted"
						}
						delete(known, change.EntityID)
						if err = frame("item", position, map[string]any{"id": change.EntityID, "@removed": dms.DeltaRemoved{Reason: reason}}); err != nil {
							return
						}
					}
				} else {
					return
				}
			case live.KindNotification:
				// Only the recipient hears of a notification, a deletion
				// included: the change names the recipient's digest.
				owner, id, _ := strings.Cut(change.EntityID, ":")
				if owner != mine {
					continue
				}
				// A stream signed in hears of every deletion, so all of a
				// person's devices stay current; an access token's, which
				// may not see every notification, only of those it showed.
				if change.Action == live.ActionDeleted {
					if token && !notes[id] {
						continue
					}
					delete(notes, id)
					if err = notificationFrame(frame, count, position, id, change.Action, nil); err != nil {
						return
					}
					continue
				}
				// A change of the whole inbox, such as marking everything
				// read, names no notification.
				if id == "" {
					if err = notificationFrame(frame, count, position, "", change.Action, nil); err != nil {
						return
					}
					continue
				}
				note, e := a.notifications(r).Get(r.Context(), subject(r), id)
				if errors.Is(e, dms.ErrNotFound) {
					continue
				}
				if e != nil {
					return
				}
				notes[note.ID] = true
				if err = notificationFrame(frame, count, position, note.ID, change.Action, &note); err != nil {
					return
				}
			case live.KindComment:
				// A comment frame names what changed for people who may read
				// its discussion; clients read the thread again.
				if a.CommentsOff {
					continue
				}
				c, visible, e := a.comments(r).Visible(r.Context(), subject(r), change.EntityID)
				if e != nil {
					return
				}
				if visible {
					if err = frame("comment", position, map[string]any{"id": c.ID, "item_id": c.ItemID, "thread_id": c.ThreadID, "action": change.Action}); err != nil {
						return
					}
				}
			case live.KindOperation, live.KindRun:
				// A run is an operation: its manager follows it as a run.
				if a.Workflows != nil {
					run, e := a.Workflows.RunUpdate(r.Context(), subject(r), change.EntityID)
					if e == nil {
						if err = frame("run", position, run); err != nil {
							return
						}
					} else if !errors.Is(e, dms.ErrForbidden) && !errors.Is(e, dms.ErrNotFound) {
						return
					}
				}
				if change.Kind == live.KindRun {
					continue
				}
				op, e := a.dms(r).Operation(r.Context(), subject(r), change.EntityID)
				if e == nil {
					if err = frame("operation", position, op); err != nil {
						return
					}
				} else if !errors.Is(e, dms.ErrForbidden) && !errors.Is(e, dms.ErrNotFound) {
					return
				}
			}
		}
		if len(known) > 10000 {
			_ = frame("resync", position, map[string]any{})
			return
		}
		if len(changes) == 500 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-keepalive.C:
			if err = frame("keepalive", position, map[string]any{}); err != nil {
				return
			}
		case <-ticker.C:
		}
	}
}

// notificationFrame sends a notification frame with the recipient's unread
// count, which a bell shows. A frame without an id tells of a change to the
// whole inbox.
func notificationFrame(frame func(string, int64, any) error, count func() (int64, error), position int64, id string, action live.Action, note *notify.View) error {
	unread, err := count()
	if err != nil {
		return err
	}
	value := map[string]any{"action": action, "unread": unread}
	if id != "" {
		value["id"] = id
	}
	if note != nil {
		value["notification"] = note
	}
	return frame("notification", position, value)
}
