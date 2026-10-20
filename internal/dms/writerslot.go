package dms

import (
	"context"
	"slices"
	"sync"
	"time"
)

// backgroundYield is how many other writers a waiting background writer
// lets go first before it takes the slot, so steady requests delay workflow
// steps but never stall them.
const backgroundYield = 8

type backgroundKey struct{}

// BackgroundWrite marks writes made with ctx as background work that
// started at since. They wait for the writer slot behind requests and other
// writes, so a burst of workflow steps cannot queue in front of people's
// edits, and among themselves the oldest work goes first, so a backlog of
// runs finishes run by run instead of advancing every run a step at a time.
func BackgroundWrite(ctx context.Context, since time.Time) context.Context {
	return context.WithValue(ctx, backgroundKey{}, since)
}

func background(ctx context.Context) (time.Time, bool) {
	since, ok := ctx.Value(backgroundKey{}).(time.Time)
	return since, ok
}

type slotWaiter struct {
	turn  chan struct{}
	since time.Time
}

// writerSlot admits one writer at a time before it takes a database
// connection: ordinary writers in arrival order, background writers by the
// start of their work.
type writerSlot struct {
	mu      sync.Mutex
	held    bool
	waiting [2][]slotWaiter // ordinary, then background writers
	skipped int             // grants made while background writers waited
}

func newWriterSlot() *writerSlot { return &writerSlot{} }

// acquire waits for the slot or for ctx to end.
func (s *writerSlot) acquire(ctx context.Context) error {
	since, deferred := background(ctx)
	priority := 0
	if deferred {
		priority = 1
	}
	s.mu.Lock()
	if !s.held {
		s.held = true
		s.mu.Unlock()
		return nil
	}
	w := slotWaiter{turn: make(chan struct{}), since: since}
	queue := s.waiting[priority]
	at := len(queue)
	if deferred {
		at, _ = slices.BinarySearchFunc(queue, since, func(q slotWaiter, t time.Time) int {
			if q.since.After(t) {
				return 1
			}
			return -1
		})
	}
	s.waiting[priority] = slices.Insert(queue, at, w)
	s.mu.Unlock()
	select {
	case <-w.turn:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		queued := slices.IndexFunc(s.waiting[priority], func(q slotWaiter) bool { return q.turn == w.turn })
		if queued >= 0 {
			s.waiting[priority] = slices.Delete(s.waiting[priority], queued, queued+1)
		}
		s.mu.Unlock()
		if queued < 0 {
			// The slot was handed over while ctx ended.
			s.release()
		}
		return ctx.Err()
	}
}

// release hands the slot to the next writer: ordinary writers first, a
// background writer once it has let backgroundYield others go first.
func (s *writerSlot) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ordinary, deferred := len(s.waiting[0]) > 0, len(s.waiting[1]) > 0
	next := -1
	switch {
	case deferred && (!ordinary || s.skipped >= backgroundYield):
		next, s.skipped = 1, 0
	case ordinary:
		next = 0
		if deferred {
			s.skipped++
		}
	}
	if next < 0 {
		s.held = false
		return
	}
	w := s.waiting[next][0]
	s.waiting[next] = s.waiting[next][1:]
	close(w.turn)
}
