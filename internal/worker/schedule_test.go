package worker

import (
	"testing"
	"time"
)

func TestScheduleRejectsImpossibleDates(t *testing.T) {
	for _, spec := range []string{"0 0 31 2 *", "0 0 31 4 *"} {
		if _, err := ParseSchedule(spec, "UTC", MissedOnce); err == nil {
			t.Fatalf("accepted impossible date %q", spec)
		}
	}
	if _, err := ParseSchedule("0 0 29 2 *", "UTC", MissedOnce); err != nil {
		t.Fatalf("rejected leap day: %v", err)
	}
}

type fixedOccurrence struct{ at time.Time }

func (s fixedOccurrence) Next(time.Time) time.Time { return s.at }

func TestScheduleDueStopsWithoutANextOccurrence(t *testing.T) {
	last := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{{}, last, last.Add(-time.Hour)} {
		s := &Schedule{Location: time.UTC, cron: fixedOccurrence{at: at}}
		if occurrence, due := s.Due(last, last.Add(time.Hour)); due || !occurrence.IsZero() {
			t.Fatalf("nonadvancing schedule: %v %v", occurrence, due)
		}
	}
	// A schedule that stops advancing after one due occurrence must also
	// return instead of looping over that occurrence forever.
	at := last.Add(time.Hour)
	s := &Schedule{Location: time.UTC, cron: fixedOccurrence{at: at}}
	if occurrence, due := s.Due(last, at.Add(time.Hour)); !due || !occurrence.Equal(at) {
		t.Fatalf("last occurrence: %v %v", occurrence, due)
	}
}
