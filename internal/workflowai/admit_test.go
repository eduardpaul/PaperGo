package workflowai

import (
	"fmt"
	"testing"
	"time"
)

func queued(n int) []queuedRequest {
	out := make([]queuedRequest, n)
	for i := range out {
		out[i] = queuedRequest{Key: fmt.Sprintf("k%d", i), Hash: fmt.Sprintf("%02d", i), Queue: "shared", Connection: "c", Model: "m"}
	}
	return out
}

func TestAdmitThresholdScheduleAndFlush(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	future := flushState{NextFlush: now.Add(time.Hour)}
	past := flushState{NextFlush: now.Add(-time.Minute)}

	plan, err := admit(now, "shared", policyThreshold, "c", "m", "0 1 * * *", "Europe/Madrid", queued(19), nil, past)
	if err != nil || len(plan.Groups) != 0 || plan.FlushSet {
		t.Fatalf("19 stay queued: %+v %v", plan, err)
	}
	plan, err = admit(now, "shared", policyThreshold, "c", "m", "", "", queued(20), nil, flushState{})
	if err != nil || len(plan.Groups) != 1 || len(plan.Groups[0]) != 20 {
		t.Fatalf("20 submit: %+v %v", plan.Groups, err)
	}
	plan, err = admit(now, "shared", policyBoth, "c", "m", "0 1 * * *", "Europe/Madrid", queued(25), nil, future)
	if err != nil || len(plan.Groups) != 1 || len(plan.Groups[0]) != 20 {
		t.Fatalf("25 without flush keeps 5: %+v %v", plan.Groups, err)
	}
	plan, err = admit(now, "shared", policyBoth, "c", "m", "0 1 * * *", "Europe/Madrid", queued(25), nil, past)
	if err != nil || len(plan.Groups) != 2 || len(plan.Groups[0]) != 20 || len(plan.Groups[1]) != 5 || !plan.FlushSet || !plan.NextFlush.After(now) {
		t.Fatalf("flush sends the remainder: %+v %v", plan, err)
	}
	plan, err = admit(now, "shared", policySchedule, "c", "m", "0 1 * * *", "UTC", queued(5), nil, future)
	if err != nil || len(plan.Groups) != 0 {
		t.Fatalf("schedule waits for the flush: %+v %v", plan.Groups, err)
	}
	plan, err = admit(now, "shared", policySchedule, "c", "m", "0 1 * * *", "Europe/Madrid", queued(5), nil, past)
	if err != nil || len(plan.Groups) != 1 || len(plan.Groups[0]) != 5 {
		t.Fatalf("daily flush sends a partial: %+v %v", plan.Groups, err)
	}
	plan, err = admit(now, "shared", policyBoth, "c", "m", "0 1 * * *", "Europe/Madrid", queued(3), nil, flushState{})
	if err != nil || len(plan.Groups) != 0 || !plan.FlushSet || !plan.NextFlush.After(now) {
		t.Fatalf("a zero next flush only schedules: %+v %v", plan, err)
	}
	blocked := []openFlight{{Tag: "flight", Status: "submitted", BatchID: "b1", Hashes: []string{"00"}, NextPoll: now.Add(time.Minute)}}
	reqs := append(queued(21), queuedRequest{Key: "extra", Hash: "00", Queue: "shared", Connection: "c", Model: "m"})
	plan, err = admit(now, "shared", policyThreshold, "c", "m", "", "", reqs, blocked, flushState{})
	if err != nil || len(plan.Groups) != 1 || len(plan.Groups[0]) != 20 || plan.Groups[0][0] == "00" {
		t.Fatalf("in flight does not block other hashes: %+v %v", plan, err)
	}
	if len(plan.Poll) != 0 {
		t.Fatalf("poll is not due: %+v", plan.Poll)
	}
	blocked[0].NextPoll = now
	plan, err = admit(now, "shared", policyThreshold, "c", "m", "", "", reqs, blocked, flushState{})
	if err != nil || len(plan.Poll) != 1 || plan.Poll[0].BatchID != "b1" {
		t.Fatalf("due poll: %+v %v", plan.Poll, err)
	}
	other := queuedRequest{Key: "other", Hash: "zz", Queue: "other", Connection: "c", Model: "m"}
	plan, err = admit(now, "shared", policyThreshold, "c", "m", "", "", append(queued(20), other), nil, flushState{})
	if err != nil || len(plan.Groups) != 1 {
		t.Fatalf("another queue stays out: %+v %v", plan.Groups, err)
	}
	for _, hash := range plan.Groups[0] {
		if hash == "zz" {
			t.Fatal("submitted a request for another queue")
		}
	}
}
