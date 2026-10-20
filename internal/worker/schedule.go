package worker

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// Missed is what a schedule does with occurrences that passed while
// nothing ran it, such as during a restart.
type Missed string

const (
	// MissedOnce runs one occurrence for any number missed.
	MissedOnce Missed = "once"
	// MissedSkip drops missed occurrences and waits for the next one.
	MissedSkip Missed = "skip"
)

// Schedule is a cron schedule in a time zone. An occurrence is identified
// by its UTC instant. Across daylight-saving changes it follows
// robfig/cron: a time the spring change skips does not occur that day, and
// a time the autumn change repeats occurs once.
type Schedule struct {
	Spec     string
	Location *time.Location
	Missed   Missed
	cron     cron.Schedule
}

// ParseSchedule parses a five-field cron spec in zone.
func ParseSchedule(spec, zone string, missed Missed) (*Schedule, error) {
	if zone == "" {
		return nil, errors.New("a schedule needs a time zone")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("time zone %s: %w", zone, err)
	}
	if missed == "" {
		missed = MissedOnce
	}
	if missed != MissedOnce && missed != MissedSkip {
		return nil, fmt.Errorf("missed must be once or skip, not %q", missed)
	}
	parsed, err := cron.ParseStandard(spec)
	if err != nil {
		return nil, fmt.Errorf("cron %q: %w", spec, err)
	}
	return &Schedule{Spec: spec, Location: location, Missed: missed, cron: parsed}, nil
}

// Next returns the first occurrence after t.
func (s *Schedule) Next(t time.Time) time.Time { return s.cron.Next(t.In(s.Location)).UTC() }

// Due returns the occurrence to run at now, given the last one that ran,
// and whether one is due. A missed occurrence runs once, or with
// MissedSkip only within a minute of its time.
func (s *Schedule) Due(last, now time.Time) (time.Time, bool) {
	next := s.Next(last)
	if next.After(now) {
		return time.Time{}, false
	}
	// The latest occurrence at or before now.
	for after := s.Next(next); !after.After(now); after = s.Next(after) {
		next = after
	}
	if s.Missed == MissedSkip && now.Sub(next) > time.Minute {
		return next, false
	}
	return next, true
}

// Window admits executions between From and To, times of day in Location.
// A window whose To is before its From spans midnight.
type Window struct {
	Location *time.Location
	From, To time.Duration
}

// ParseWindow parses a window from "15:04" times in zone.
func ParseWindow(zone, from, to string) (*Window, error) {
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		return nil, fmt.Errorf("a window needs a valid time zone, not %q", zone)
	}
	parse := func(v string) (time.Duration, error) {
		t, err := time.Parse("15:04", v)
		if err != nil {
			return 0, fmt.Errorf("window times are HH:MM, not %q", v)
		}
		return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
	}
	w := &Window{Location: location}
	if w.From, err = parse(from); err != nil {
		return nil, err
	}
	if w.To, err = parse(to); err != nil {
		return nil, err
	}
	return w, w.validate()
}

func (w *Window) validate() error {
	if w.Location == nil || w.From == w.To {
		return errors.New("a window needs a time zone and different start and end times")
	}
	return nil
}

// Open reports whether t is inside the window, and otherwise when it next
// opens.
func (w *Window) Open(t time.Time) (bool, time.Time) {
	local := t.In(w.Location)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.Location)
	since := local.Sub(midnight)
	inside := since >= w.From && since < w.To
	if w.To < w.From {
		inside = since >= w.From || since < w.To
	}
	if inside {
		return true, time.Time{}
	}
	opens := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.Location).Add(w.From)
	if !opens.After(local) {
		opens = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, w.Location).Add(w.From)
	}
	return false, opens.UTC()
}
