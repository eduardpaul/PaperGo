package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"papergo/internal/dal/resources"
	"time"

	"github.com/google/uuid"
	"papergo/internal/calendar"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/operations"
)

// Runtime dependencies belong to the provider; persistent storage is generic.
type iCalendarRuntime struct {
	validateURL func(context.Context, string, func(string) bool) (string, error)
	download    func(context.Context, string, string, string, func(string) bool) (calendar.Download, error)
}

func newICalendarRuntime() *iCalendarRuntime {
	f := calendar.NewFetcher()
	return &iCalendarRuntime{calendar.ValidateURL, f.Fetch}
}

type iCalendarConfig struct {
	URL string `json:"url"`
}
type iCalendarState struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

func eventRecipe() TemplateInput {
	return TemplateInput{Key: "event", Name: "Event", Fields: []CreateField{
		{Key: "event_description", Label: "Description", Type: "note"}, {Key: "event_location", Label: "Location", Type: "text"},
		{Key: "event_start", Label: "Start", Type: "datetime", Indexed: true}, {Key: "event_end", Label: "End", Type: "datetime", Indexed: true},
		{Key: "event_all_day", Label: "All day", Type: "boolean", Indexed: true},
		{Key: "event_start_date", Label: "Start date", Type: "date", Indexed: true}, {Key: "event_end_date", Label: "End date (exclusive)", Type: "date", Indexed: true},
		{Key: "event_timezone", Label: "Time zone", Type: "text"}, {Key: "event_recurrence", Label: "Recurrence rule", Type: "text"},
		{Key: "event_exdates", Label: "Excluded dates", Type: "note"}, {Key: "event_rdates", Label: "Additional dates", Type: "note"},
	}}
}
func (s *Service) ensureEventType(ctx context.Context, subject string, c *resources.Resource) (*catalog.ContentType, error) {
	ids, err := s.provisionCollectionTypes(ctx, subject, CollectionTemplate{ContentTypes: []TemplateInput{eventRecipe()}})
	if err != nil {
		return nil, err
	}
	if _, err = s.attachContentType(ctx, subject, c.ID, c.Version, AttachContentTypeInput{ContentTypeID: ids[0]}); err != nil {
		return nil, err
	}
	typ, err := s.DAL.Catalog().ContentType(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	if err = s.validateEventFields(ctx, typ); err != nil {
		return nil, err
	}
	// A saved calendar presentation is optional, and uses the existing layout.
	// Existing default views and ordinary content types stay as configured.
	return typ, nil
}
func (s *Service) validateEventFields(ctx context.Context, typ *catalog.ContentType) error {
	defs, err := s.typeDefinitions(ctx, typ)
	if err != nil {
		return err
	}
	byKey := definitionMap(defs)
	for _, f := range eventRecipe().Fields {
		d := byKey[f.Key]
		if d == nil || d.Type != f.Type || d.Options.Multiple {
			return invalid("Event fields required by an active integration cannot be removed or change type: " + f.Key)
		}
	}
	return nil
}
func (s *Service) validateIntegrationSchema(ctx context.Context, c *resources.Resource) error {
	active, err := s.DAL.Integrations().Exists(ctx, integrations.Filter{ContainerID: c.ID})
	if err != nil || !active {
		return err
	}
	typ, err := s.eventType(ctx, c.ID)
	if err != nil {
		return invalid("remove list integrations before detaching the Event content type")
	}
	return s.validateEventFields(ctx, typ)
}

func (s *Service) refreshIntegrationOperation(ctx context.Context, op *operations.Operation) error {
	id, err := decodeIntegrationRefresh(op.Payload)
	if err != nil {
		return err
	}
	var source *integrations.Integration
	lease := uuid.NewString()
	err = s.write(ctx, func(t *Service) error {
		current, e := t.DAL.Operations().Find(ctx, op.ID)
		if e != nil {
			return e
		}
		if current.Status != operations.StatusPending {
			return nil
		}
		if e = validateIntegrationRefresh(ctx, t, op.CreatedBy, op.ContainerID, op.Payload); e != nil {
			return e
		}
		found, e := t.DAL.Integrations().Find(ctx, id)
		if e != nil {
			return e
		}
		if e = t.checkIntegrationLease(&found); e != nil {
			return Retry(e, time.Until(*found.LeaseUntil))
		}
		attempt := time.Now().UTC()
		until := attempt.Add(integrationLease)
		found.LeaseToken, found.LeaseUntil, found.LastAttemptAt = lease, &until, &attempt
		if e = t.DAL.Integrations().Save(ctx, &found); e != nil {
			return e
		}
		source = &found
		return nil
	})
	if err != nil || source == nil {
		return err
	}
	defer func() {
		// Release only our own lease. Cancellation leaves recovery to expiry if the
		// deployment is shutting down before this short cleanup can commit.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.write(cleanup, func(t *Service) error {
			return t.DAL.Integrations().ReleaseLease(cleanup, id, lease)
		})
	}()
	if s.integrations == nil {
		return errors.New("list integrations are not configured")
	}
	configJSON, err := s.integrations.secrets.Decrypt(source.ContainerID, source.ConfigCiphertext)
	if err != nil {
		return err
	}
	var config iCalendarConfig
	var state iCalendarState
	if json.Unmarshal([]byte(configJSON), &config) != nil || json.Unmarshal(source.State, &state) != nil {
		return errors.New("invalid iCalendar integration configuration")
	}
	trusted, err := s.trustedHost(ctx)
	if err != nil {
		return err
	}
	downloaded, err := s.integrations.ical.download(ctx, config.URL, state.ETag, state.LastModified, trusted)
	if err != nil {
		return err
	}
	var events []calendar.Event
	if !downloaded.NotModified {
		events, err = calendar.Parse(downloaded.Body)
		if err != nil {
			return err
		}
	}
	err = s.write(ctx, func(t *Service) error {
		ctx, e := t.operationContext(ctx, op)
		if e != nil {
			return e
		}
		row, e := t.integration(ctx, op.CreatedBy, source.ContainerID, id, "manage")
		if e != nil {
			return e
		}
		// A worker whose lease expired cannot apply a stale feed.
		if row.LeaseToken != lease || row.Version != source.Version || row.LeaseUntil == nil || !row.LeaseUntil.After(time.Now().UTC()) {
			return Retry(ErrIntegrationBusy, time.Minute)
		}
		current, e := t.DAL.Operations().Find(ctx, op.ID)
		if e != nil {
			return e
		}
		if current.Status != operations.StatusPending {
			return nil
		}
		count := row.ItemCount
		if !downloaded.NotModified {
			t.integrationWrite = true
			count, e = t.reconcileCalendar(ctx, op.CreatedBy, row, events)
			if e != nil {
				return errors.New("calendar snapshot could not be applied to the list; existing events were retained")
			}
		}
		success := time.Now().UTC()
		row.LastSuccessAt, row.Error, row.ItemCount, row.LeaseToken, row.LeaseUntil = &success, "", count, "", nil
		if !downloaded.NotModified {
			row.State, _ = json.Marshal(iCalendarState{downloaded.ETag, downloaded.LastModified})
		}
		if e = t.DAL.Integrations().Save(ctx, row); e != nil {
			return e
		}
		result, _ := json.Marshal(map[string]any{"integration_id": id, "item_count": count, "not_modified": downloaded.NotModified})
		completed := time.Now().UTC()
		current.Claim = op.Claim
		current.Status, current.Progress, current.CompletedAt, current.Result = operations.StatusSucceeded, 100, &completed, result
		return t.SaveOperation(ctx, &current)
	})
	return err
}
func (s *Service) reconcileCalendar(ctx context.Context, subject string, source *integrations.Integration, events []calendar.Event) (int, error) {
	typ, err := s.eventType(ctx, source.ContainerID)
	if err != nil {
		return 0, err
	}
	if err = s.validateEventFields(ctx, typ); err != nil {
		return 0, err
	}
	rows, err := s.DAL.Integrations().Items(ctx, source.ID)
	if err != nil {
		return 0, err
	}
	byUID := map[string]*integrations.Item{}
	for i := range rows {
		byUID[rows[i].ExternalID] = &rows[i]
	}
	present := map[string]bool{}
	count := 0
	for _, event := range events {
		if event.Cancelled() {
			continue
		}
		present[event.UID] = true
		count++
		title, values, err := event.Content()
		if err != nil {
			return 0, err
		}
		snapshot, err := json.Marshal(event)
		if err != nil {
			return 0, err
		}
		if err = s.SetIntegrationData(IntegrationKindICalendar, snapshot); err != nil {
			return 0, err
		}
		row := byUID[event.UID]
		if row == nil {
			namespace, err := uuid.Parse(source.Generation)
			if err != nil {
				return 0, err
			}
			itemID := uuid.NewSHA1(namespace, []byte(event.UID)).String()
			clean := map[string]any{}
			for k, v := range values {
				if v != nil {
					clean[k] = v
				}
			}
			item, err := s.create(ctx, subject, source.ContainerID, CreateResource{ID: &itemID, Kind: "item", Name: title, ContentTypeID: typ.ID, Values: clean}, nil)
			if err != nil {
				return 0, err
			}
			if _, err = s.DAL.Integrations().AddItem(ctx, integrations.Item{ItemID: item.ID, IntegrationID: source.ID, ExternalID: event.UID, ProviderKind: IntegrationKindICalendar, ManagedFields: append([]string{"$name", "$content_type"}, calendar.ManagedFields...)}); err != nil {
				return 0, err
			}
			continue
		}
		item, err := s.DAL.Resources().Resource(ctx, row.ItemID)
		if err != nil {
			return 0, err
		}
		if item.DeletedAt != nil {
			item, err = s.RestoreDeleted(ctx, subject, source.ContainerID, item.ID, false)
			if err != nil {
				return 0, err
			}
		}
		if err = s.overlayHead(ctx, item); err != nil {
			return 0, err
		}
		head, err := s.revision(ctx, *item.HeadRevisionID)
		if err != nil {
			return 0, err
		}
		data, err := s.revisionIntegrationData(ctx, head.ID)
		if err != nil {
			return 0, err
		}
		changed := item.Name != title || data == nil || !sameJSON(json.RawMessage(snapshot), data.Data)
		for key, value := range values {
			if !sameJSON(value, item.Values[key]) {
				changed = true
			}
		}
		if changed {
			if _, err = s.update(ctx, subject, item.ID, item.Version, UpdateResource{Name: &title, Values: &values}); err != nil {
				return 0, err
			}
		}
	}
	s.pendingIntegrationData = nil
	// This removal runs only after every event write succeeded, in the same
	// transaction. An invalid custom field rolls back the entire snapshot.
	for _, row := range rows {
		if present[row.ExternalID] {
			continue
		}
		item, err := s.DAL.Resources().Resource(ctx, row.ItemID)
		if err != nil {
			return 0, err
		}
		if item.DeletedAt == nil {
			if err = s.delete(ctx, subject, item.ID, item.Version, false); err != nil {
				return 0, err
			}
		}
	}
	return count, nil
}

// CalendarOccurrences and CalendarExport read the revision selected by ordinary
// publication permissions, so draft exceptions cannot leak through calendar UI.
type CalendarQuery struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Surface string    `json:"surface,omitempty"`
	Query   QuerySpec `json:"query"`
}

type CalendarOccurrence struct {
	calendar.Occurrence
	Item *resources.Resource `json:"item"`
}

func (s *Service) CalendarOccurrences(ctx context.Context, subject, list string, from, to time.Time, surface string, query ...QuerySpec) ([]CalendarOccurrence, error) {
	if !from.Before(to) || to.Sub(from) > 93*24*time.Hour {
		return nil, invalid("calendar range must be between 1 and 93 days")
	}
	return read(ctx, s, func(t *Service) ([]CalendarOccurrence, error) {
		if _, err := t.integrationList(ctx, subject, list, "read"); err != nil {
			return nil, err
		}
		rows, err := t.calendarItems(ctx, subject, list, surface, query...)
		if err != nil {
			return nil, err
		}
		out := []CalendarOccurrence{}
		for _, row := range rows {
			event, err := t.calendarEventForItem(ctx, row)
			if err != nil {
				return nil, err
			}
			occ, err := event.Occurrences(ctx, from, to, 10000-len(out))
			if err != nil {
				return nil, invalid(err.Error())
			}
			for _, o := range occ {
				out = append(out, CalendarOccurrence{o, row})
			}
		}
		return out, nil
	})
}
func (s *Service) CalendarExport(ctx context.Context, subject, list, surface string) ([]byte, error) {
	return read(ctx, s, func(t *Service) ([]byte, error) {
		if _, err := t.integrationList(ctx, subject, list, "read"); err != nil {
			return nil, err
		}
		rows, err := t.calendarItems(ctx, subject, list, surface)
		if err != nil {
			return nil, err
		}
		events := []calendar.Event{}
		ids := []string{}
		for _, row := range rows {
			event, err := t.calendarEventForItem(ctx, row)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
			ids = append(ids, row.ID)
		}
		return calendar.Export(events, ids)
	})
}
func (s *Service) calendarItems(ctx context.Context, subject, list, surface string, query ...QuerySpec) ([]*resources.Resource, error) {
	typ, err := s.eventType(ctx, list)
	if err != nil {
		return nil, err
	}
	out := []*resources.Resource{}
	spec := QuerySpec{ContentTypeID: typ.ID}
	if len(query) > 0 {
		spec = query[0]
		if spec.ContentTypeID != "" && spec.ContentTypeID != typ.ID {
			return out, nil
		}
		spec.ContentTypeID = typ.ID
		spec.GroupBy = ""
		spec.Sort = nil
	}
	after := ""
	inspected := 0
	for {
		page, err := s.Query(ctx, subject, list, QueryRequest{Surface: surface, Query: spec, After: after, Limit: 100})
		if err != nil {
			return nil, err
		}
		inspected += len(page.Data)
		if inspected > 10000 {
			return nil, invalid("calendar contains more than 10000 event series")
		}
		for _, item := range page.Data {
			if item.Values["event_start"] != nil || item.Values["event_start_date"] != nil {
				out = append(out, item)
			}
		}
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	return out, nil
}
func (s *Service) calendarEventForItem(ctx context.Context, r *resources.Resource) (calendar.Event, error) {
	// GetSurface overlays the selected head/published revision ID and values.
	var snapshot json.RawMessage
	selected := r.HeadRevisionID
	if selected == nil {
		selected = r.PublishedRevisionID
	}
	if selected != nil {
		rev, err := s.revision(ctx, *selected)
		if err != nil {
			return calendar.Event{}, err
		}
		data, err := s.revisionIntegrationData(ctx, rev.ID)
		if err != nil {
			return calendar.Event{}, err
		}
		if data != nil && data.ProviderKind == IntegrationKindICalendar {
			snapshot = data.Data
		}
	}
	event, err := calendar.FromValues(r.Name, r.Values, snapshot)
	if err != nil {
		return event, invalid(fmt.Sprintf("event %s has invalid calendar fields", r.ID))
	}
	return event, nil
}

func mustIntegrationConfig(raw string) json.RawMessage {
	b, _ := json.Marshal(iCalendarConfig{URL: raw})
	return b
}

func decodeICalendarConfig(raw json.RawMessage) (iCalendarConfig, error) {
	var config iCalendarConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&config) != nil || config.URL == "" || dec.Decode(new(any)) != io.EOF {
		return config, invalid("iCalendar configuration requires an object with an HTTPS url")
	}
	return config, nil
}
