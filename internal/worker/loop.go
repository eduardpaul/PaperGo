package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"
	"time"

	"papergo/internal/dal/activitystore"
)

// idle is the longest a worker sleeps with nothing due: a safety net for
// wakes another process would give.
const idle = time.Minute

// worker is the running state of one definition.
type worker struct {
	rt     *Runtime
	def    *Definition
	state  atomic.Value // State
	flush  atomic.Value // string: the last administrative flush request handled
	active atomic.Int64
}

func (w *worker) current() State {
	if s, ok := w.state.Load().(State); ok {
		return s
	}
	return Running
}

// claiming reports whether the worker may start executions at now, and
// otherwise when to look again.
func (w *worker) claiming(now time.Time) (bool, time.Time) {
	if w.current() != Running {
		return false, time.Time{}
	}
	if w.def.Window != nil {
		return w.def.Window.Open(now)
	}
	return true, time.Time{}
}

// immediate claims ready work of the worker's kinds, one execution at a
// time, until ctx ends.
func (w *worker) immediate(ctx, work context.Context) {
	for ctx.Err() == nil {
		wake := w.rt.woken()
		open, opens := w.claiming(w.rt.now())
		if !open {
			sleep(ctx, wake, until(opens, w.rt.now()))
			continue
		}
		w.active.Add(1)
		worked, err := w.rt.svc.RunOperation(work, w.def.Kinds)
		w.active.Add(-1)
		if err != nil && ctx.Err() == nil {
			w.rt.log.Error("operation failed", "worker", w.def.Name, "error", err)
			sleep(ctx, wake, time.Second)
			continue
		}
		if worked {
			continue
		}
		next, err := w.rt.svc.NextOperationAttempt(ctx, w.def.Kinds)
		wait := idle
		if err == nil && next != nil {
			wait = min(wait, next.Sub(w.rt.now()))
		}
		sleep(ctx, wake, wait)
	}
}

// drain is a persisted cutoff: a group past its deadline or flushed runs
// every member created by the cutoff, in bounded executions, even when
// fewer than the threshold remain, and a restart continues it.
type drain struct {
	Cutoff time.Time `json:"cutoff"`
}

// groups admits work in groups by the worker's policy until ctx ends.
func (w *worker) groups(ctx, work context.Context) {
	records := activitystore.New(w.rt.svc.DAL.SQLDB())
	for ctx.Err() == nil {
		wake := w.rt.woken()
		now := w.rt.now()
		next := now.Add(idle)
		open, opens := w.claiming(now)
		if !open {
			sleep(ctx, wake, until(opens, now))
			continue
		}
		flushed, flushNext, err := w.flushDue(ctx, records, now)
		if err != nil && ctx.Err() == nil {
			w.rt.log.Error("worker flush schedule failed", "worker", w.def.Name, "error", err)
		}
		if !flushNext.IsZero() && flushNext.Before(next) {
			next = flushNext
		}
		ran, err := w.admit(ctx, work, records, now, flushed, &next)
		if err != nil && ctx.Err() == nil {
			w.rt.log.Error("operation group failed", "worker", w.def.Name, "error", err)
			sleep(ctx, wake, time.Second)
			continue
		}
		if ran {
			continue
		}
		if attempt, err := w.rt.svc.NextOperationAttempt(ctx, w.def.Kinds); err == nil && attempt != nil && attempt.Before(next) {
			next = *attempt
		}
		sleep(ctx, wake, until(next, w.rt.now()))
	}
}

// admit runs every eligible group once and reports whether it ran any.
// next moves to when the earliest group below its threshold becomes due.
func (w *worker) admit(ctx, work context.Context, records *activitystore.Store, now time.Time, flushed bool, next *time.Time) (bool, error) {
	a := w.def.Admission
	groups, err := w.rt.svc.ReadyOperationGroups(ctx, w.def.Kinds)
	if err != nil {
		return false, err
	}
	ran := false
	for _, g := range groups {
		key := drainKey(g.Kind, g.Key)
		marker, err := records.Get(ctx, drainNamespace, w.def.Name, key)
		if err != nil {
			return ran, err
		}
		var through *time.Time
		switch {
		case marker != nil:
			var d drain
			if err = json.Unmarshal(marker.Payload, &d); err != nil {
				return ran, err
			}
			through = &d.Cutoff
		case flushed || (a.Policy == MaxWait || a.Policy == ThresholdOrMaxWait) && !now.Before(g.Oldest.Add(a.MaxWait)):
			// Freeze the backlog: later arrivals keep their own deadline.
			cutoff := now.UTC()
			raw, _ := json.Marshal(drain{Cutoff: cutoff})
			if err = records.Put(ctx, activitystore.Record{Namespace: drainNamespace, Scope: w.def.Name, Key: key, Payload: raw}); err != nil {
				return ran, err
			}
			through = &cutoff
		case (a.Policy == Threshold || a.Policy == ThresholdOrMaxWait) && g.Inputs >= a.Threshold:
		default:
			if a.Policy == MaxWait || a.Policy == ThresholdOrMaxWait {
				if due := g.Oldest.Add(a.MaxWait); due.Before(*next) {
					*next = due
				}
			}
			continue
		}
		w.active.Add(1)
		worked, err := w.rt.svc.RunOperationGroup(work, g.Kind, g.Key, through, a.Batch)
		w.active.Add(-1)
		if err != nil {
			return ran, err
		}
		ran = ran || worked
		if through != nil && !worked {
			// The frozen backlog is done.
			expired := now.UTC()
			if err = records.Put(ctx, activitystore.Record{Namespace: drainNamespace, Scope: w.def.Name, Key: key, Payload: json.RawMessage(`{}`), ExpiresAt: &expired}); err != nil {
				return ran, err
			}
		}
	}
	return ran, nil
}

// flushDue reports whether a flush is due now, from the schedule or an
// administrative flush, recording its occurrence, and when the schedule
// next occurs.
func (w *worker) flushDue(ctx context.Context, records *activitystore.Store, now time.Time) (bool, time.Time, error) {
	due := false
	if requested, _ := w.flush.Load().(string); requested != "" && requested != w.flushHandled(ctx, records) {
		if err := w.recordFlush(ctx, records, "admin", requested); err != nil {
			return false, time.Time{}, err
		}
		due = true
	}
	s := w.def.Admission.Flush
	if s == nil {
		return due, time.Time{}, nil
	}
	last, err := w.lastOccurrence(ctx, records)
	if err != nil {
		return due, time.Time{}, err
	}
	if last.IsZero() {
		// A new schedule starts at its next occurrence.
		last = now
		if err = w.recordFlush(ctx, records, "cron", last.UTC().Format(time.RFC3339Nano)); err != nil {
			return due, time.Time{}, err
		}
	}
	occurrence, ok := s.Due(last, now)
	if !occurrence.IsZero() {
		// Handled or skipped, the occurrence is past.
		if err = w.recordFlush(ctx, records, "cron", occurrence.Format(time.RFC3339Nano)); err != nil {
			return due, time.Time{}, err
		}
		last = occurrence
	}
	return due || ok, s.Next(last), nil
}

func (w *worker) lastOccurrence(ctx context.Context, records *activitystore.Store) (time.Time, error) {
	r, err := records.Get(ctx, cronNamespace, w.def.Name, "cron")
	if err != nil || r == nil {
		return time.Time{}, err
	}
	var v string
	if err = json.Unmarshal(r.Payload, &v); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, v)
}

func (w *worker) flushHandled(ctx context.Context, records *activitystore.Store) string {
	r, err := records.Get(ctx, cronNamespace, w.def.Name, "admin")
	if err != nil || r == nil {
		return ""
	}
	var v string
	_ = json.Unmarshal(r.Payload, &v)
	return v
}

func (w *worker) recordFlush(ctx context.Context, records *activitystore.Store, key, value string) error {
	raw, _ := json.Marshal(value)
	return records.Put(ctx, activitystore.Record{Namespace: cronNamespace, Scope: w.def.Name, Key: key, Payload: raw})
}

// Activity record namespaces of worker state.
const (
	drainNamespace   = "worker.drain"
	cronNamespace    = "worker.cron"
	controlNamespace = "worker.control"
	statusNamespace  = "worker.status"
)

func drainKey(kind, group string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + group))
	return hex.EncodeToString(sum[:])
}

// until is how long to wait for t; zero means a minute.
func until(t, now time.Time) time.Duration {
	if t.IsZero() {
		return idle
	}
	return t.Sub(now)
}
