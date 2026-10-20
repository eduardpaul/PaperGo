package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/operations"
)

// State is what a control asks a worker to do.
type State string

const (
	// Running admits and executes work.
	Running State = "running"
	// Paused admits nothing new; executions that run finish, and a group
	// policy's deadlines keep counting.
	Paused State = "paused"
	// Draining admits nothing new while executions that run finish, before
	// a planned stop.
	Draining State = "draining"
)

func (s State) Valid() bool { return s == Running || s == Paused || s == Draining }

// Control is the desired state of a worker, written by the admin command
// and read by every process. Flush, when it changes, makes every group of
// the worker eligible once.
type Control struct {
	State State  `json:"state"`
	Flush string `json:"flush,omitempty"`
}

// Status is what a worker last reported.
type Status struct {
	Worker    string    `json:"worker"`
	State     State     `json:"state"`
	Pending   int64     `json:"pending"`
	Active    int64     `json:"active"`
	UpdatedAt time.Time `json:"updated_at"`
}

// control reads the worker's control and reports its status every
// ControlPoll until ctx ends.
func (w *worker) control(ctx context.Context) {
	records := activitystore.New(w.rt.svc.DAL.SQLDB())
	for {
		if err := w.poll(ctx, records); err != nil && ctx.Err() == nil {
			w.rt.log.Error("worker control failed", "worker", w.def.Name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.rt.ControlPoll):
		}
	}
}

func (w *worker) poll(ctx context.Context, records *activitystore.Store) error {
	c, err := ReadControl(ctx, records, w.def.Name)
	if err != nil {
		return err
	}
	previous, _ := w.flush.Load().(string)
	if c.State != w.current() || c.Flush != previous {
		w.state.Store(c.State)
		w.flush.Store(c.Flush)
		w.rt.Wake()
	}
	pending, err := w.rt.svc.DAL.Operations().Count(ctx, operations.Filter{Kinds: w.def.Kinds, Statuses: []operations.Status{operations.StatusPending}})
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(Status{Worker: w.def.Name, State: c.State, Pending: pending, Active: w.active.Load(), UpdatedAt: w.rt.now().UTC()})
	return records.Put(ctx, activitystore.Record{Namespace: statusNamespace, Scope: "installation", Key: w.def.Name, Payload: raw})
}

// ReadControl returns a worker's control; a worker nobody controlled runs.
func ReadControl(ctx context.Context, records *activitystore.Store, name string) (Control, error) {
	r, err := records.Get(ctx, controlNamespace, "installation", name)
	if err != nil || r == nil {
		return Control{State: Running}, err
	}
	var c Control
	if err = json.Unmarshal(r.Payload, &c); err != nil {
		return Control{}, err
	}
	if !c.State.Valid() {
		c.State = Running
	}
	return c, nil
}

// SetControl changes a worker's desired state; running processes apply it
// within their control poll.
func SetControl(ctx context.Context, db *sql.DB, name string, state State) error {
	if !state.Valid() {
		return fmt.Errorf("unknown worker state %q", state)
	}
	records := activitystore.New(db)
	c, err := ReadControl(ctx, records, name)
	if err != nil {
		return err
	}
	c.State = state
	return putControl(ctx, records, name, c)
}

// RequestFlush makes every group of a worker eligible once.
func RequestFlush(ctx context.Context, db *sql.DB, name string) error {
	records := activitystore.New(db)
	c, err := ReadControl(ctx, records, name)
	if err != nil {
		return err
	}
	c.Flush = time.Now().UTC().Format(time.RFC3339Nano)
	return putControl(ctx, records, name, c)
}

func putControl(ctx context.Context, records *activitystore.Store, name string, c Control) error {
	raw, _ := json.Marshal(c)
	return records.Put(ctx, activitystore.Record{Namespace: controlNamespace, Scope: "installation", Key: name, Payload: raw})
}

// Statuses returns what every worker last reported.
func Statuses(ctx context.Context, db *sql.DB) ([]Status, error) {
	rows, err := activitystore.New(db).List(ctx, statusNamespace, "installation", "pending", "", 100)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(rows))
	for _, r := range rows {
		var s Status
		if err = json.Unmarshal(r.Payload, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
