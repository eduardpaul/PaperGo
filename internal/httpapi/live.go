package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"papergo/ent/livechange"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strconv"
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
	position, err := a.DMS.LivePosition(r.Context(), after)
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
	for {
		if err = identity.RequireCredential(r.Context(), a.DMS.Directory().DB, subject(r)); err != nil {
			_ = frame("reauthenticate", position, map[string]any{})
			return
		}
		changes, e := a.DMS.LiveChanges(r.Context(), position)
		if e != nil {
			return
		}
		last := map[string]int{}
		key := func(changeKind livechange.Kind, id string) string {
			if changeKind == livechange.KindAccess {
				return "access"
			}
			return string(changeKind) + ":" + id
		}
		for i, change := range changes {
			last[key(change.Kind, change.EntityID)] = i
		}
		for i, change := range changes {
			if err = identity.RequireCredential(r.Context(), a.DMS.Directory().DB, subject(r)); err != nil {
				_ = frame("reauthenticate", position, map[string]any{})
				return
			}
			position = change.ID
			if last[key(change.Kind, change.EntityID)] != i {
				continue
			}
			switch change.Kind {
			case livechange.KindAccess:
				if err = frame("resync", position, map[string]any{}); err != nil {
					return
				}
			case livechange.KindItem:
				item, e := a.DMS.Get(r.Context(), subject(r), change.EntityID)
				if e == nil {
					known[item.ID] = true
					if err = frame("item", position, map[string]any{"id": item.ID, "action": change.Action, "item": item}); err != nil {
						return
					}
				} else if errors.Is(e, dms.ErrForbidden) || errors.Is(e, dms.ErrNotFound) {
					if known[change.EntityID] {
						reason := "changed"
						if change.Action == "deleted" {
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
			case livechange.KindOperation:
				op, e := a.DMS.Operation(r.Context(), subject(r), change.EntityID)
				if e == nil {
					if err = frame("operation", position, op); err != nil {
						return
					}
				} else if !errors.Is(e, dms.ErrForbidden) && !errors.Is(e, dms.ErrNotFound) {
					return
				}
			case livechange.KindRun:
				if a.Runner == nil {
					continue
				}
				run, e := a.Runner.RunUpdate(r.Context(), subject(r), change.EntityID)
				if e == nil {
					if err = frame("run", position, run); err != nil {
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
