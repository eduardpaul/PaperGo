package dms

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// queue blocks one writer per name on s, in order, and returns the order in
// which they get the slot once the holder releases it.
func queue(t *testing.T, s *writerSlot, names []string, ctxs map[string]context.Context) []string {
	t.Helper()
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	for i, name := range names {
		ctx := ctxs[name]
		if ctx == nil {
			ctx = testContext
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.acquire(ctx); err != nil {
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			s.release()
		}()
		// Each writer is queued before the next arrives.
		deadline := time.Now().Add(5 * time.Second)
		for waiters(s) != i+1 {
			if time.Now().After(deadline) {
				t.Fatalf("%s did not queue", name)
			}
			time.Sleep(time.Millisecond)
		}
	}
	s.release()
	wg.Wait()
	return order
}

func waiters(s *writerSlot) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiting[0]) + len(s.waiting[1])
}

func TestWriterSlotLetsOrdinaryWritesPassBackgroundSteps(t *testing.T) {
	s := newWriterSlot()
	if err := s.acquire(testContext); err != nil {
		t.Fatal(err)
	}
	bg := BackgroundWrite(testContext, time.Now())
	got := queue(t, s, []string{"step1", "step2", "edit1", "edit2"}, map[string]context.Context{"step1": bg, "step2": bg})
	want := []string{"edit1", "edit2", "step1", "step2"}
	if !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestWriterSlotGivesBackgroundStepsATurn(t *testing.T) {
	s := newWriterSlot()
	if err := s.acquire(testContext); err != nil {
		t.Fatal(err)
	}
	names := []string{"step"}
	for i := range backgroundYield + 2 {
		names = append(names, "edit"+string(rune('a'+i)))
	}
	got := queue(t, s, names, map[string]context.Context{"step": BackgroundWrite(testContext, time.Now())})
	if got[backgroundYield] != "step" {
		t.Fatalf("background step got turn %d of %v, want after %d others", slices.Index(got, "step"), got, backgroundYield)
	}
}

func TestWriterSlotCancelledWaiterLeavesTheQueue(t *testing.T) {
	s := newWriterSlot()
	if err := s.acquire(testContext); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(BackgroundWrite(testContext, time.Now()), 20*time.Millisecond)
	defer cancel()
	if err := s.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued writer ignored its deadline", err)
	}
	s.release()
	if waiters(s) != 0 || s.held {
		t.Fatal("cancelled writer kept its place or the slot stayed held")
	}
	if err := s.acquire(testContext); err != nil {
		t.Fatal(err)
	}
	s.release()
}

func TestWriterSlotServesOlderBackgroundWorkFirst(t *testing.T) {
	s := newWriterSlot()
	if err := s.acquire(testContext); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := queue(t, s, []string{"later", "earliest", "earlier"}, map[string]context.Context{
		"later":    BackgroundWrite(testContext, start.Add(2*time.Second)),
		"earliest": BackgroundWrite(testContext, start),
		"earlier":  BackgroundWrite(testContext, start.Add(time.Second)),
	})
	if want := []string{"earliest", "earlier", "later"}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}
