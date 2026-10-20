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

func TestWindowsFollowLocalTimeAcrossDST(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, now, next string
		open                      bool
	}{
		{name: "spring inside", from: "09:00", to: "17:00", now: "2026-03-29T09:30:00+02:00", open: true},
		{name: "spring before", from: "09:00", to: "17:00", now: "2026-03-29T08:30:00+02:00", next: "2026-03-29T09:00:00+02:00"},
		{name: "spring end", from: "09:00", to: "17:00", now: "2026-03-29T17:00:00+02:00", next: "2026-03-30T09:00:00+02:00"},
		{name: "next day spring", from: "09:00", to: "17:00", now: "2026-03-28T20:00:00+01:00", next: "2026-03-29T09:00:00+02:00"},
		{name: "autumn before", from: "09:00", to: "17:00", now: "2026-10-25T08:30:00+01:00", next: "2026-10-25T09:00:00+01:00"},
		{name: "autumn inside", from: "09:00", to: "17:00", now: "2026-10-25T16:30:00+01:00", open: true},
		{name: "next day autumn", from: "09:00", to: "17:00", now: "2026-10-24T20:00:00+02:00", next: "2026-10-25T09:00:00+01:00"},
		{name: "overnight spring", from: "22:00", to: "06:00", now: "2026-03-29T05:30:00+02:00", open: true},
		{name: "overnight end", from: "22:00", to: "06:00", now: "2026-10-25T06:00:00+01:00", next: "2026-10-25T22:00:00+01:00"},
		{name: "skipped start", from: "02:30", to: "04:00", now: "2026-03-29T01:30:00+01:00", next: "2026-03-29T03:00:00+02:00"},
		{name: "skipped window", from: "02:10", to: "02:20", now: "2026-03-29T01:30:00+01:00", next: "2026-03-30T02:10:00+02:00"},
		{name: "repeated start", from: "02:00", to: "02:15", now: "2026-10-25T02:30:00+02:00", next: "2026-10-25T02:00:00+01:00"},
		{name: "repeated hour inside", from: "02:00", to: "02:15", now: "2026-10-25T02:05:00+01:00", open: true},
		{name: "fractional minute", from: "09:00", to: "17:00", now: "2026-03-29T08:59:59.123+02:00", next: "2026-03-29T09:00:00+02:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ParseWindow("Europe/Madrid", tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.Parse(time.RFC3339Nano, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			open, next := w.Open(now)
			if open != tc.open {
				t.Fatalf("open = %v, want %v", open, tc.open)
			}
			if tc.open {
				if !next.IsZero() {
					t.Fatal("open window returned an opening time", next)
				}
				return
			}
			want, err := time.Parse(time.RFC3339, tc.next)
			if err != nil {
				t.Fatal(err)
			}
			if !next.Equal(want) {
				t.Fatalf("opens at %v, want %v", next, want)
			}
		})
	}
}

func TestWindowRejectsInvalidClockTimes(t *testing.T) {
	for _, from := range []time.Duration{-time.Minute, 24 * time.Hour, time.Hour + time.Second} {
		w := &Window{Location: time.UTC, From: from, To: 2 * time.Hour}
		if err := w.validate(); err == nil {
			t.Fatalf("accepted invalid start %v", from)
		}
		w.From, w.To = w.To, w.From
		if err := w.validate(); err == nil {
			t.Fatalf("accepted invalid end %v", w.To)
		}
	}
}
