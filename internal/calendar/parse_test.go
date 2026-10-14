package calendar

import (
	"context"
	"encoding/json"
	"net/netip"
	"papergo/internal/integration"
	"strings"
	"testing"
	"time"
)

func feed(events string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Tests//EN\r\n" + strings.ReplaceAll(events, "\n", "\r\n") + "END:VCALENDAR\r\n")
}
func TestRecurrenceDSTExdatesAndMovedInstances(t *testing.T) {
	body := feed("BEGIN:VEVENT\nUID:weekly\nSUMMARY:Weekly\\, meeting\nDTSTART;TZID=Europe/Madrid:20261019T090000\nDTEND;TZID=Europe/Madrid:20261019T100000\nRRULE:FREQ=WEEKLY;COUNT=4\nEXDATE;TZID=Europe/Madrid:20261026T090000,20261109T090000\nEND:VEVENT\nBEGIN:VEVENT\nUID:weekly\nRECURRENCE-ID;TZID=Europe/Madrid:20261102T090000\nSUMMARY:Moved\nDTSTART;TZID=Europe/Madrid:20261103T110000\nDTEND;TZID=Europe/Madrid:20261103T120000\nEND:VEVENT\n")
	events, err := Parse(body)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 2, 0)
	occ, err := events[0].Occurrences(context.Background(), from, to, 100)
	if err != nil || len(occ) != 2 || occ[0].Start.UTC().Format(time.RFC3339) != "2026-10-19T07:00:00Z" || occ[1].Start.UTC().Format(time.RFC3339) != "2026-11-03T10:00:00Z" {
		t.Fatal(occ, err)
	}
	title, values, err := events[0].Content()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events[0])
	reconstructed, err := FromValues(title, values, raw)
	if err != nil {
		t.Fatal(err)
	}
	round, err := reconstructed.Occurrences(context.Background(), from, to, 100)
	if err != nil || len(round) != 2 || !round[1].Start.Equal(occ[1].Start) {
		t.Fatal(round, err)
	}
	exported, err := Export(events, []string{"local-id"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported)
	if err != nil || parsed[0].UID != "local-id@papergo" {
		t.Fatal(string(exported), err)
	}
}
func TestInvalidSnapshotsAndLimits(t *testing.T) {
	base := "BEGIN:VEVENT\nUID:a\nDTSTART:20261014T100000Z\nEND:VEVENT\n"
	for _, body := range [][]byte{
		[]byte("not a calendar"), feed(strings.ReplaceAll(base, "UID:a\n", "")), feed(base + base), append(feed(base), []byte("garbage")...),
		feed(strings.ReplaceAll(base, "DTSTART:", "DTSTART;TZID=Not/AZone:")),
		feed(strings.ReplaceAll(base, "END:VEVENT", "RDATE;VALUE=PERIOD:20261014T100000Z/PT1H\nEND:VEVENT")),
		feed(strings.ReplaceAll(base, "END:VEVENT", "EXRULE:FREQ=WEEKLY\nEND:VEVENT")),
		feed(strings.ReplaceAll(base, "END:VEVENT", "RRULE:FREQ=SECONDLY\nEND:VEVENT")),
		feed(strings.ReplaceAll(base, "END:VEVENT", "DTEND:20261014T090000Z\nEND:VEVENT")),
		feed(strings.ReplaceAll(base, "END:VEVENT", "RECURRENCE-ID:20261014T100000Z\nEND:VEVENT")),
		feed(strings.Repeat(strings.ReplaceAll(base, "UID:a", "UID:a"), MaxComponents+1)),
	} {
		if _, err := Parse(body); err == nil {
			t.Fatalf("invalid feed accepted: %.180s", body)
		}
	}
	if events, err := Parse(feed("")); err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
	// A cancelled identity need not contain start/end or a title.
	if events, err := Parse(feed("BEGIN:VEVENT\nUID:a\nSTATUS:CANCELLED\nEND:VEVENT\n")); err != nil || !events[0].Cancelled() {
		t.Fatal(events, err)
	}
}
func TestAllDayAndDST(t *testing.T) {
	events, err := Parse(feed("BEGIN:VEVENT\nUID:all\nSUMMARY:Holiday\nDTSTART;VALUE=DATE:20261014\nDTEND;VALUE=DATE:20261016\nRRULE:FREQ=YEARLY;COUNT=2\nEND:VEVENT\nBEGIN:VEVENT\nUID:dst\nDTSTART;TZID=Europe/Madrid:20261019T090000\nDTEND;TZID=Europe/Madrid:20261019T100000\nRRULE:FREQ=WEEKLY;COUNT=2\nEND:VEVENT\n"))
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 1, 0)
	occ, err := events[0].Occurrences(context.Background(), from, to, 20)
	if err != nil || len(occ) != 2 || !occ[0].AllDay || occ[0].EndDate != "2026-10-16" {
		t.Fatal(occ, err)
	}
	occ, err = events[1].Occurrences(context.Background(), from, to, 20)
	if err != nil || len(occ) != 2 || occ[0].Start.UTC().Hour() != 7 || occ[1].Start.UTC().Hour() != 8 {
		t.Fatal(occ, err)
	}
}
func TestSecretsAndPublicDestinations(t *testing.T) {
	key := make([]byte, 32)
	s, err := integration.NewSecrets(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := s.Encrypt("list", "https://example.com/secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Decrypt("other-list", ciphertext); err == nil {
		t.Fatal("encryption was not list bound")
	}
	if raw, err := s.Decrypt("list", ciphertext); err != nil || raw != "https://example.com/secret" {
		t.Fatal(err)
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "::ffff:127.0.0.1", "::1", "169.254.169.254", "100.64.0.1", "fc00::1", "2002:a00:1::", "192.0.2.1"} {
		if PublicIP(netip.MustParseAddr(ip)) {
			t.Fatal("allowed", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !PublicIP(netip.MustParseAddr(ip)) {
			t.Fatal("denied", ip)
		}
	}
	for _, raw := range []string{"http://example.com/feed", "https://u:p@example.com/feed", "https://example.com:8443/feed", "https://example.com/#fragment"} {
		if _, err := ParseURL(raw); err == nil {
			t.Fatal("allowed", raw)
		}
	}
}
