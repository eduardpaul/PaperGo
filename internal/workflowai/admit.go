package workflowai

import (
	"fmt"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// Admission policies. threshold submits every full group of batchSize.
// schedule submits only when the flush is due, including a partial group.
// both submits full groups on every pass and a partial group on the flush.
// An in-flight batch does not block another group.
const (
	policyThreshold = "threshold"
	policySchedule  = "schedule"
	policyBoth      = "both"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type queuedRequest struct {
	Key, Hash, Queue, Connection, Model string
	Cached                              bool
	Attempts                            int
}

type openFlight struct {
	Tag, Status, BatchID string
	Hashes               []string
	NextPoll             time.Time
}

// pollTarget is one submitted batch the coordinator reads by its provider id.
type pollTarget struct {
	Tag     string `json:"tag"`
	BatchID string `json:"batch_id"`
}

type flushState struct {
	NextFlush time.Time
}

type admitPlan struct {
	Groups    [][]string
	Reconcile []string
	Poll      []pollTarget
	Complete  []string
	NextFlush time.Time
	FlushSet  bool
}

func admit(now time.Time, queue, policy, connection, model, cronExpr, zone string, requests []queuedRequest, flights []openFlight, state flushState) (admitPlan, error) {
	if policy == "" {
		policy = policyBoth
	}
	if policy != policyThreshold && policy != policySchedule && policy != policyBoth {
		return admitPlan{}, fmt.Errorf("policy must be threshold, schedule or both")
	}
	var plan admitPlan
	flush := false
	if policy != policyThreshold {
		if cronExpr == "" {
			cronExpr = "0 1 * * *"
		}
		if zone == "" {
			zone = "Europe/Madrid"
		}
		if state.NextFlush.IsZero() {
			next, err := nextSlot(cronExpr, zone, now)
			if err != nil {
				return admitPlan{}, err
			}
			plan.NextFlush, plan.FlushSet = next, true
		} else if !state.NextFlush.After(now) {
			next, err := nextSlot(cronExpr, zone, now)
			if err != nil {
				return admitPlan{}, err
			}
			plan.NextFlush, plan.FlushSet = next, true
			flush = true
		}
	}
	blocked := map[string]bool{}
	for _, flight := range flights {
		switch flight.Status {
		case "uncertain":
			plan.Reconcile = append(plan.Reconcile, flight.Tag)
			for _, hash := range flight.Hashes {
				blocked[hash] = true
			}
		case "submitted":
			if !flight.NextPoll.After(now) {
				plan.Poll = append(plan.Poll, pollTarget{Tag: flight.Tag, BatchID: flight.BatchID})
			}
			for _, hash := range flight.Hashes {
				blocked[hash] = true
			}
		}
	}
	seen := map[string]bool{}
	var hashes []string
	for _, req := range requests {
		if req.Queue != queue || req.Connection != connection || req.Model != model {
			continue
		}
		if req.Cached {
			plan.Complete = append(plan.Complete, req.Key)
			continue
		}
		if req.Hash == "" || blocked[req.Hash] || seen[req.Hash] {
			continue
		}
		seen[req.Hash] = true
		hashes = append(hashes, req.Hash)
	}
	take := func(n int) {
		if n > len(hashes) {
			n = len(hashes)
		}
		if n == 0 {
			return
		}
		plan.Groups = append(plan.Groups, append([]string(nil), hashes[:n]...))
		hashes = hashes[n:]
	}
	switch policy {
	case policyThreshold:
		for len(hashes) >= batchSize {
			take(batchSize)
		}
	case policySchedule:
		if flush {
			for len(hashes) >= batchSize {
				take(batchSize)
			}
			take(len(hashes))
		}
	default:
		for len(hashes) >= batchSize {
			take(batchSize)
		}
		if flush {
			take(len(hashes))
		}
	}
	return plan, nil
}

func nextSlot(cronExpr, zone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown time zone %s", zone)
	}
	sched, err := cronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid flush schedule %q", cronExpr)
	}
	return sched.Next(after.In(loc)).UTC(), nil
}
