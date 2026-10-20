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
	s := &Schedule{Spec: spec, Location: location, Missed: missed, cron: parsed}
	if s.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf("cron %q has no future occurrence", spec)
	}
	return s, nil
}

// Next returns the first occurrence after t.
func (s *Schedule) Next(t time.Time) time.Time { return s.cron.Next(t.In(s.Location)).UTC() }

// Due returns the occurrence to run at now, given the last one that ran,
// and whether one is due. A missed occurrence runs once, or with
// MissedSkip only within a minute of its time.
func (s *Schedule) Due(last, now time.Time) (time.Time, bool) {
	next := s.Next(last)
	if next.IsZero() || !next.After(last) || next.After(now) {
		return time.Time{}, false
	}
	// The latest occurrence at or before now.
	for {
		after := s.Next(next)
		if after.IsZero() || !after.After(next) || after.After(now) {
			break
		}
		next = after
	}
	if s.Missed == MissedSkip && now.Sub(next) > time.Minute {
		return next, false
	}
	return next, true
}

// Window admits executions between From and To, minute-precision local
// times of day in Location.
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
	if w.From < 0 || w.From >= 24*time.Hour || w.To < 0 || w.To >= 24*time.Hour {
		return errors.New("window times must be within one day")
	}
	if w.From%time.Minute != 0 || w.To%time.Minute != 0 {
		return errors.New("window times must use whole minutes")
	}
	return nil
}

// Open reports whether t is inside the window, and otherwise when it next
// opens.
func (w *Window) Open(t time.Time) (bool, time.Time) {
	if w.contains(t) {
		return true, time.Time{}
	}
	// Windows have minute precision. Walk minute boundaries in real time
	// so the next opening also accounts for skipped and repeated local hours.
	local := t.In(w.Location)
	opens := local.Add(time.Minute - time.Duration(local.Second())*time.Second - time.Duration(local.Nanosecond()))
	for !w.contains(opens) {
		opens = opens.Add(time.Minute)
	}
	return false, opens.UTC()
}

func (w *Window) contains(t time.Time) bool {
	local := t.In(w.Location)
	clock := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute
	if w.To < w.From {
		return clock >= w.From || clock < w.To
	}
	return clock >= w.From && clock < w.To
}
