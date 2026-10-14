package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	ical "github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Event stores only supported event content. Invitations, alarms, attachments,
// conferencing and provider-specific properties never enter the replica.
type Event struct {
	UID        string            `json:"uid"`
	Master     *ical.Component   `json:"master"`
	Exceptions []*ical.Component `json:"exceptions"`
}

var ManagedFields = []string{"event_description", "event_location", "event_start", "event_end", "event_all_day", "event_start_date", "event_end_date", "event_timezone", "event_recurrence", "event_exdates", "event_rdates"}
var kept = []string{"UID", "SUMMARY", "DESCRIPTION", "LOCATION", "DTSTART", "DTEND", "DURATION", "STATUS", "RRULE", "EXDATE", "RDATE", "RECURRENCE-ID"}

func parseError() error {
	return errors.New("invalid or unsupported iCalendar snapshot; existing events were retained")
}
func isDate(p *ical.Prop) bool { return p != nil && p.ValueType() == ical.ValueDate }
func datetime(p *ical.Prop, loc *time.Location) (time.Time, error) {
	if p == nil {
		return time.Time{}, parseError()
	}
	if z := p.Params.Get("TZID"); z != "" {
		if _, err := time.LoadLocation(z); err != nil {
			return time.Time{}, parseError()
		}
	}
	if isDate(p) && (len(p.Value) != 8 || p.Params.Get("TZID") != "") {
		return time.Time{}, parseError()
	}
	if !isDate(p) && len(p.Value) != 15 && !(len(p.Value) == 16 && strings.HasSuffix(p.Value, "Z")) {
		return time.Time{}, parseError()
	}
	if strings.HasSuffix(p.Value, "Z") && p.Params.Get("TZID") != "" {
		return time.Time{}, parseError()
	}
	t, err := p.DateTime(loc)
	if err != nil {
		return time.Time{}, parseError()
	}
	return t, nil
}
func dates(c *ical.Component, key string, start *ical.Prop, loc *time.Location) ([]time.Time, error) {
	var out []time.Time
	for _, p := range c.Props.Values(key) {
		if p.ValueType() == ical.ValuePeriod || isDate(&p) != isDate(start) {
			return nil, parseError()
		}
		for _, v := range strings.Split(p.Value, ",") {
			q := p
			q.Value = v
			t, err := datetime(&q, loc)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}
func recurrence(c *ical.Component) (*rrule.Set, error) {
	p := c.Props.Get("DTSTART")
	start, err := datetime(p, time.UTC)
	if err != nil {
		return nil, err
	}
	set := &rrule.Set{}
	set.DTStart(start)
	set.RDate(start)
	if rule := c.Props.Get("RRULE"); rule != nil {
		if len(rule.Value) > 4096 {
			return nil, parseError()
		}
		opt, err := rrule.StrToROptionInLocation(rule.Value, start.Location())
		if err != nil {
			return nil, parseError()
		}
		// Sub-day recurrence and extension rules are outside this integration's
		// bounded calendar model. Do not silently truncate them.
		if opt.Freq > rrule.DAILY || opt.Byeaster != nil || opt.Interval < 0 || opt.Count < 0 {
			return nil, parseError()
		}
		opt.Dtstart = start
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, parseError()
		}
		set.RRule(r)
	}
	for _, key := range []string{"EXDATE", "RDATE"} {
		ts, err := dates(c, key, p, start.Location())
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if key == "EXDATE" {
				set.ExDate(t)
			} else {
				set.RDate(t)
			}
		}
	}
	return set, nil
}
func Parse(body []byte) ([]Event, error) {
	if len(body) > MaxBytes {
		return nil, errors.New("calendar feed exceeds 10 MiB")
	}
	if !utf8.Valid(body) {
		return nil, parseError()
	}
	// Bound component nesting before the recursive decoder sees the feed.
	depth := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		upper := strings.ToUpper(strings.TrimSuffix(string(line), "\r"))
		if strings.HasPrefix(upper, "BEGIN:") {
			depth++
			if depth > 16 {
				return nil, parseError()
			}
		}
		if strings.HasPrefix(upper, "END:") {
			depth--
			if depth < 0 {
				return nil, parseError()
			}
		}
	}
	if depth != 0 {
		return nil, parseError()
	}
	dec := ical.NewDecoder(bytes.NewReader(body))
	cal, err := dec.Decode()
	if err != nil {
		return nil, parseError()
	}
	if _, err = dec.Decode(); !errors.Is(err, io.EOF) {
		return nil, parseError()
	}
	if cal.Props.Get("VERSION") == nil || cal.Props.Get("VERSION").Value != "2.0" {
		return nil, parseError()
	}
	events := cal.Events()
	if len(events) > MaxComponents {
		return nil, errors.New("calendar feed exceeds 2000 VEVENT components")
	}
	byUID := map[string]*Event{}
	identities := map[string]bool{}
	for _, e := range events {
		c := e.Component
		for _, key := range []string{"UID", "SUMMARY", "DESCRIPTION", "LOCATION", "DTSTART", "DTEND", "DURATION", "STATUS", "RRULE", "RECURRENCE-ID"} {
			if len(c.Props.Values(key)) > 1 {
				return nil, parseError()
			}
		}
		uid, err := c.Props.Text("UID")
		if err != nil || strings.TrimSpace(uid) == "" || len(uid) > 4096 {
			return nil, parseError()
		}
		if len(c.Props.Values("EXRULE")) > 0 {
			return nil, parseError()
		}
		// STATUS may be absent.
		status, err := e.Status()
		if err != nil {
			return nil, parseError()
		}
		cancelled := status == ical.EventCancelled
		rp := c.Props.Get("RECURRENCE-ID")
		rid := ""
		if rp != nil {
			if rp.Params.Get("RANGE") != "" {
				return nil, parseError()
			}
			t, err := datetime(rp, time.UTC)
			if err != nil {
				return nil, err
			}
			rid = t.UTC().Format(time.RFC3339) + fmt.Sprint(isDate(rp))
		}
		identity := uid + "\x00" + rid
		if identities[identity] {
			return nil, parseError()
		}
		identities[identity] = true
		if !cancelled {
			p := c.Props.Get("DTSTART")
			start, err := datetime(p, time.UTC)
			if err != nil {
				return nil, err
			}
			if start.Year() < 1900 || start.Year() > 2200 {
				return nil, parseError()
			}
			if rp != nil && isDate(rp) != isDate(p) {
				return nil, parseError()
			}
			if c.Props.Get("DTEND") != nil && c.Props.Get("DURATION") != nil {
				return nil, parseError()
			}
			if end := c.Props.Get("DTEND"); end != nil {
				if isDate(end) != isDate(p) {
					return nil, parseError()
				}
				if _, err = datetime(end, start.Location()); err != nil {
					return nil, err
				}
			}
			end, err := e.DateTimeEnd(start.Location())
			if err != nil || end.Before(start) || (isDate(p) && !end.After(start)) || end.Sub(start) > 366*24*time.Hour {
				return nil, parseError()
			}
			for _, key := range []string{"SUMMARY", "DESCRIPTION", "LOCATION"} {
				if _, err := c.Props.Text(key); err != nil {
					return nil, parseError()
				}
			}
			if _, err = recurrence(c); err != nil {
				return nil, err
			}
			if rp != nil && (c.Props.Get("RRULE") != nil || len(c.Props.Values("EXDATE")) > 0 || len(c.Props.Values("RDATE")) > 0) {
				return nil, parseError()
			}
		}
		clean := ical.NewComponent("VEVENT")
		for _, key := range kept {
			if ps := c.Props.Values(key); len(ps) > 0 {
				clean.Props[key] = ps
			}
		}
		if status != "" {
			clean.Props.SetText("STATUS", string(status))
		}
		series := byUID[uid]
		if series == nil {
			series = &Event{UID: uid, Exceptions: []*ical.Component{}}
			byUID[uid] = series
		}
		if rp == nil {
			series.Master = clean
		} else {
			series.Exceptions = append(series.Exceptions, clean)
		}
	}
	out := []Event{}
	for _, e := range byUID {
		if e.Master == nil {
			return nil, parseError()
		}
		if !e.Cancelled() {
			p := e.Master.Props.Get("DTSTART")
			start, _ := datetime(p, time.UTC)
			for _, ex := range e.Exceptions {
				rp := ex.Props.Get("RECURRENCE-ID")
				if isDate(p) != isDate(rp) {
					return nil, parseError()
				}
				if _, err := datetime(rp, start.Location()); err != nil {
					return nil, err
				}
			}
		}
		sort.Slice(e.Exceptions, func(i, j int) bool {
			return e.Exceptions[i].Props.Get("RECURRENCE-ID").Value < e.Exceptions[j].Props.Get("RECURRENCE-ID").Value
		})
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}
func (e Event) Cancelled() bool {
	p := e.Master.Props.Get("STATUS")
	return p != nil && strings.EqualFold(p.Value, "CANCELLED")
}
func (e Event) Content() (string, map[string]any, error) {
	if e.Cancelled() {
		return "", nil, parseError()
	}
	c := e.Master
	p := c.Props.Get("DTSTART")
	start, err := datetime(p, time.UTC)
	if err != nil {
		return "", nil, err
	}
	ev := ical.Event{Component: c}
	end, err := ev.DateTimeEnd(start.Location())
	if err != nil {
		return "", nil, parseError()
	}
	title, _ := c.Props.Text("SUMMARY")
	if strings.TrimSpace(title) == "" {
		title = "Untitled event"
	}
	desc, _ := c.Props.Text("DESCRIPTION")
	location, _ := c.Props.Text("LOCATION")
	values := map[string]any{"event_description": desc, "event_location": location, "event_start": start.UTC().Format(time.RFC3339), "event_end": end.UTC().Format(time.RFC3339), "event_all_day": isDate(p), "event_timezone": start.Location().String(), "event_start_date": nil, "event_end_date": nil, "event_recurrence": nil, "event_exdates": nil, "event_rdates": nil}
	if isDate(p) {
		values["event_start_date"] = start.Format(time.DateOnly)
		values["event_end_date"] = end.Format(time.DateOnly)
	}
	if rp := c.Props.Get("RRULE"); rp != nil {
		values["event_recurrence"] = rp.Value
	}
	for _, key := range []string{"EXDATE", "RDATE"} {
		ts, err := dates(c, key, p, start.Location())
		if err != nil {
			return "", nil, err
		}
		if len(ts) > 0 {
			texts := []string{}
			for _, t := range ts {
				if isDate(p) {
					texts = append(texts, t.Format(time.DateOnly))
				} else {
					texts = append(texts, t.Format(time.RFC3339))
				}
			}
			values[map[string]string{"EXDATE": "event_exdates", "RDATE": "event_rdates"}[key]] = strings.Join(texts, ",")
		}
	}
	return title, values, nil
}

type Occurrence struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	AllDay    bool      `json:"all_day"`
	StartDate string    `json:"start_date,omitempty"`
	EndDate   string    `json:"end_date,omitempty"`
}

func occurrence(c *ical.Component, start time.Time, key string) (Occurrence, error) {
	e := Event{Master: c}
	name, _, err := e.Content()
	if err != nil {
		return Occurrence{}, err
	}
	original, _ := datetime(c.Props.Get("DTSTART"), time.UTC)
	end, err := (&ical.Event{Component: c}).DateTimeEnd(original.Location())
	if err != nil {
		return Occurrence{}, parseError()
	}
	allDay := isDate(c.Props.Get("DTSTART"))
	finish := start.Add(end.Sub(original))
	if allDay {
		days := int(end.Sub(original) / (24 * time.Hour))
		finish = start.AddDate(0, 0, days)
	}
	out := Occurrence{Key: key, Name: name, Start: start, End: finish, AllDay: allDay}
	if allDay {
		out.StartDate = start.Format(time.DateOnly)
		out.EndDate = finish.Format(time.DateOnly)
	}
	return out, nil
}

// Occurrences expands only a bounded visible window and checks cancellation
// while iterating. Exceptions are addressed by original recurrence identity.
func (e Event) Occurrences(ctx context.Context, from, to time.Time, limit int) ([]Occurrence, error) {
	out := []Occurrence{}
	if e.Cancelled() {
		return out, nil
	}
	start, err := datetime(e.Master.Props.Get("DTSTART"), time.UTC)
	if err != nil {
		return nil, err
	}
	exceptions := map[string]*ical.Component{}
	for _, ex := range e.Exceptions {
		t, err := datetime(ex.Props.Get("RECURRENCE-ID"), start.Location())
		if err != nil {
			return nil, err
		}
		exceptions[t.UTC().Format(time.RFC3339)] = ex
	}
	add := func(c *ical.Component, t time.Time, key string) error {
		if p := c.Props.Get("STATUS"); p != nil && p.Value == "CANCELLED" {
			return nil
		}
		o, err := occurrence(c, t, key)
		if err != nil {
			return err
		}
		if o.Start.Before(to) && !o.Start.Before(from) {
			out = append(out, o)
		}
		if len(out) > limit {
			return errors.New("calendar occurrence limit exceeded")
		}
		return nil
	}
	set, err := recurrence(e.Master)
	if err != nil {
		return nil, err
	}
	next := set.Iterator()
	for i := 0; ; i++ {
		if i > 200000 {
			return nil, errors.New("calendar recurrence iteration limit exceeded")
		}
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		t, ok := next()
		if !ok || !t.Before(to) {
			break
		}
		key := t.UTC().Format(time.RFC3339)
		if _, replaced := exceptions[key]; !replaced {
			if err := add(e.Master, t, key); err != nil {
				return nil, err
			}
		}
	}
	// Moved instances are included by their new time even when their original
	// recurrence identity lies outside the visible window or is an EXDATE.
	for key, c := range exceptions {
		if p := c.Props.Get("STATUS"); p != nil && p.Value == "CANCELLED" {
			continue
		}
		t, err := datetime(c.Props.Get("DTSTART"), start.Location())
		if err != nil {
			return nil, err
		}
		if err := add(c, t, key); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}
func Export(events []Event, ids []string) ([]byte, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText("VERSION", "2.0")
	cal.Props.SetText("PRODID", "-//PaperGo//Calendar//EN")
	for i, e := range events {
		for _, c := range append([]*ical.Component{e.Master}, e.Exceptions...) {
			copy := ical.NewComponent("VEVENT")
			for k, ps := range c.Props {
				copy.Props[k] = append([]ical.Prop(nil), ps...)
			}
			copy.Props.SetText("UID", ids[i]+"@papergo")
			if copy.Props.Get("DTSTAMP") == nil {
				copy.Props.SetDateTime("DTSTAMP", time.Now().UTC())
			}
			cal.Children = append(cal.Children, copy)
		}
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, parseError()
	}
	return buf.Bytes(), nil
}

// FromValues applies the selected revision's editable fields while keeping its
// saved exceptions. It also supports independent, locally created Event items.
func FromValues(name string, values map[string]any, snapshot []byte) (Event, error) {
	event := Event{Master: ical.NewComponent("VEVENT"), Exceptions: []*ical.Component{}}
	if len(snapshot) > 0 {
		if err := json.Unmarshal(snapshot, &event); err != nil || event.Master == nil || event.Master.Props == nil {
			return event, parseError()
		}
	}
	c := event.Master
	text := func(key string) string { v, _ := values[key].(string); return v }
	zone := text("event_timezone")
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return event, parseError()
	}
	allDay, _ := values["event_all_day"].(bool)
	var start, end time.Time
	if allDay {
		start, err = time.ParseInLocation(time.DateOnly, text("event_start_date"), time.UTC)
		if err != nil {
			return event, parseError()
		}
		if text("event_end_date") == "" {
			end = start.AddDate(0, 0, 1)
		} else {
			end, err = time.ParseInLocation(time.DateOnly, text("event_end_date"), time.UTC)
		}
	} else {
		start, err = time.Parse(time.RFC3339, text("event_start"))
		if err != nil {
			return event, parseError()
		}
		start = start.In(loc)
		if text("event_end") == "" {
			end = start
		} else {
			end, err = time.Parse(time.RFC3339, text("event_end"))
			end = end.In(loc)
		}
	}
	if err != nil || end.Before(start) || (allDay && !end.After(start)) {
		return event, parseError()
	}
	c.Props.SetText("SUMMARY", name)
	c.Props.SetText("DESCRIPTION", text("event_description"))
	c.Props.SetText("LOCATION", text("event_location"))
	c.Props.Del("DTSTART")
	c.Props.Del("DTEND")
	c.Props.Del("DURATION")
	c.Props.Del("STATUS")
	if allDay {
		c.Props.SetDate("DTSTART", start)
		c.Props.SetDate("DTEND", end)
	} else {
		c.Props.SetDateTime("DTSTART", start)
		c.Props.SetDateTime("DTEND", end)
	}
	oldRule := ""
	if p := c.Props.Get("RRULE"); p != nil {
		oldRule = p.Value
	}
	c.Props.Del("RRULE")
	if rule := text("event_recurrence"); rule != "" {
		p := ical.NewProp("RRULE")
		p.Value = rule
		c.Props.Set(p)
	}
	if oldRule != text("event_recurrence") {
		event.Exceptions = []*ical.Component{}
	}
	for key, field := range map[string]string{"EXDATE": "event_exdates", "RDATE": "event_rdates"} {
		c.Props.Del(key)
		if text(field) == "" {
			continue
		}
		for _, v := range strings.Split(text(field), ",") {
			p := ical.NewProp(key)
			if allDay {
				d, e := time.Parse(time.DateOnly, v)
				if e != nil {
					return event, parseError()
				}
				p.SetDate(d)
			} else {
				d, e := time.Parse(time.RFC3339, v)
				if e != nil {
					return event, parseError()
				}
				p.SetDateTime(d.In(loc))
			}
			c.Props.Add(p)
		}
	}
	if _, err = recurrence(c); err != nil {
		return event, err
	}
	return event, nil
}
