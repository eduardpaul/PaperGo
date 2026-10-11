package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"papergo/ent"
	"papergo/ent/resource"
	"papergo/ent/syncstate"
	"papergo/ent/synctoken"
	"papergo/ent/syncview"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrDeltaExpired = errors.New("delta token has expired; synchronize without a token")

type DeltaRemoved struct {
	Reason string `json:"reason"`
}
type DeltaEntry struct {
	*ent.Resource
	ID      string        `json:"id"`
	Removed *DeltaRemoved `json:"@removed,omitempty"`
}
type DeltaResult struct {
	Data       []DeltaEntry
	NextToken  string
	DeltaToken string
}
type visibleDeltaItem struct {
	item        *ent.Resource
	fingerprint string
}

// Delta stores visibility/fingerprint ranges, rather than retained metadata
// snapshots. A token names a durable position in the caller's collection view.
// Materialized page content has a short, separate retention period.
func (s *Service) Delta(ctx context.Context, subject, collectionID, deltaToken, continuation string, limit int) (DeltaResult, error) {
	var out DeltaResult
	if deltaToken != "" && continuation != "" {
		return out, invalid("use either a delta token or a continuation")
	}
	err := s.write(ctx, func(t *Service) error {
		now := time.Now().UTC()
		if continuation != "" {
			parts, err := base64.RawURLEncoding.DecodeString(continuation)
			if err != nil || len(parts) > 128 {
				return invalid("invalid delta continuation")
			}
			values := strings.Split(string(parts), ".")
			if len(values) != 2 {
				return invalid("invalid delta continuation")
			}
			offset, err := strconv.Atoi(values[1])
			if err != nil || offset < 0 {
				return invalid("invalid delta continuation")
			}
			token, _, err := t.deltaAnchor(ctx, subject, collectionID, values[0], now)
			if err != nil {
				return err
			}
			if !token.PageExpiresAt.After(now) {
				return ErrDeltaExpired
			}
			var entries []DeltaEntry
			if err = json.Unmarshal(token.Changes, &entries); err != nil {
				return err
			}
			if offset > len(entries) {
				if len(entries) == 0 {
					return ErrDeltaExpired
				}
				return invalid("invalid delta continuation offset")
			}
			out, err = t.deltaPage(ctx, subject, token, entries, offset, limit)
			return err
		}
		var anchor *ent.SyncToken
		var view *ent.SyncView
		var err error
		if deltaToken != "" {
			anchor, view, err = t.deltaAnchor(ctx, subject, collectionID, deltaToken, now)
			if err != nil {
				return err
			}
		} else {
			view, err = t.Client.SyncView.Query().Where(syncview.SubjectEQ(subject), syncview.ContainerIDEQ(collectionID)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
		}
		current, err := t.deltaVisible(ctx, subject, collectionID)
		if err != nil {
			if anchor == nil || !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
				return err
			}
			current = map[string]visibleDeltaItem{}
		}
		if view == nil {
			view, err = t.Client.SyncView.Create().SetSubject(subject).SetContainerID(collectionID).Save(ctx)
			if err != nil {
				return err
			}
		}
		latest, err := t.deltaState(ctx, view.ID, view.Sequence)
		if err != nil {
			return err
		}
		changed := false
		for id, item := range current {
			if latest[id] != item.fingerprint {
				changed = true
				break
			}
		}
		if len(latest) != len(current) {
			changed = true
		}
		if changed || view.Sequence == 0 {
			next := view.Sequence + 1
			for id, fingerprint := range latest {
				if item, exists := current[id]; !exists || item.fingerprint != fingerprint {
					if err = t.Client.SyncState.Update().Where(syncstate.ViewIDEQ(view.ID), syncstate.ItemIDEQ(id), syncstate.ToSequenceIsNil()).SetToSequence(next).Exec(ctx); err != nil {
						return err
					}
				}
			}
			for id, item := range current {
				if latest[id] != item.fingerprint {
					if err = t.Client.SyncState.Create().SetViewID(view.ID).SetItemID(id).SetFromSequence(next).SetFingerprint(item.fingerprint).Exec(ctx); err != nil {
						return err
					}
				}
			}
			view, err = t.Client.SyncView.UpdateOne(view).SetSequence(next).Save(ctx)
			if err != nil {
				return err
			}
		}
		baseline := map[string]string{}
		if anchor != nil {
			baseline, err = t.deltaState(ctx, view.ID, anchor.Sequence)
			if err != nil {
				return err
			}
		}
		entries := []DeltaEntry{}
		for id, item := range current {
			if baseline[id] != item.fingerprint {
				entries = append(entries, DeltaEntry{Resource: item.item, ID: id})
			}
		}
		for id := range baseline {
			if _, exists := current[id]; exists {
				continue
			}
			reason := "changed"
			row, e := t.Client.Resource.Get(ctx, id)
			if ent.IsNotFound(e) || e == nil && row.DeletedAt != nil {
				reason = "deleted"
			} else if e != nil {
				return e
			}
			entries = append(entries, DeltaEntry{ID: id, Removed: &DeltaRemoved{Reason: reason}})
		}
		sortDelta(entries)
		if anchor != nil && len(entries) == 0 && anchor.Sequence == view.Sequence {
			if err = t.Client.SyncToken.UpdateOne(anchor).SetExpiresAt(now.Add(30 * 24 * time.Hour)).Exec(ctx); err != nil {
				return err
			}
			out = DeltaResult{Data: []DeltaEntry{}, DeltaToken: anchor.ID}
			return nil
		}
		payload, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		anchor, err = t.Client.SyncToken.Create().SetViewID(view.ID).SetSequence(view.Sequence).SetChanges(payload).SetPageExpiresAt(now.Add(5 * time.Minute)).SetExpiresAt(now.Add(30 * 24 * time.Hour)).Save(ctx)
		if err != nil {
			return err
		}
		out, err = t.deltaPage(ctx, subject, anchor, entries, 0, limit)
		return err
	})
	return out, err
}
func (s *Service) deltaAnchor(ctx context.Context, subject, collectionID, id string, now time.Time) (*ent.SyncToken, *ent.SyncView, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil, invalid("invalid delta token")
	}
	token, err := s.Client.SyncToken.Get(ctx, id)
	if ent.IsNotFound(err) || err == nil && !token.ExpiresAt.After(now) {
		return nil, nil, ErrDeltaExpired
	}
	if err != nil {
		return nil, nil, err
	}
	view, err := s.Client.SyncView.Get(ctx, token.ViewID)
	if err != nil {
		return nil, nil, err
	}
	if view.Subject != subject || view.ContainerID != collectionID {
		return nil, nil, ErrDeltaExpired
	}
	return token, view, nil
}
func (s *Service) deltaState(ctx context.Context, viewID string, sequence int64) (map[string]string, error) {
	rows, err := s.Client.SyncState.Query().Where(syncstate.ViewIDEQ(viewID), syncstate.FromSequenceLTE(sequence), syncstate.Or(syncstate.ToSequenceIsNil(), syncstate.ToSequenceGT(sequence))).All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, row := range rows {
		out[row.ItemID] = row.Fingerprint
	}
	return out, nil
}
func (s *Service) deltaVisible(ctx context.Context, subject, collectionID string) (map[string]visibleDeltaItem, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	c, err := s.authorize(ctx, subject, collectionID, "read")
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
		return nil, invalid("delta requires a list or library")
	}
	out := map[string]visibleDeltaItem{}
	after := ""
	for {
		rows, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(collectionID), resource.KindIn(resource.KindItem, resource.KindFolder), resource.DeletedAtIsNil(), resource.IDGT(after), permissionPredicate(ctx, subject, "read"), func(sel *entsql.Selector) {
			visible, args := visibleSQL(ctx, sel.C(resource.FieldID), subject, "auto")
			sel.Where(entsql.ExprP(visible, args...))
		}).Order(ent.Asc(resource.FieldID)).Limit(500).All(ctx)
		if err != nil {
			return nil, err
		}
		if _, err = s.overlayPage(ctx, subject, "auto", rows, nil); err != nil {
			return nil, err
		}
		for _, item := range rows {
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256(raw)
			out[item.ID] = visibleDeltaItem{item, hex.EncodeToString(hash[:])}
		}
		if len(rows) < 500 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	return out, nil
}
func (s *Service) deltaPage(ctx context.Context, subject string, token *ent.SyncToken, entries []DeltaEntry, offset, limit int) (DeltaResult, error) {
	end := offset + pageSize(limit)
	if end > len(entries) {
		end = len(entries)
	}
	out := DeltaResult{Data: []DeltaEntry{}}
	for _, entry := range entries[offset:end] {
		if entry.Removed == nil {
			// A cached page cannot release content after its access is revoked.
			if _, err := s.Get(ctx, subject, entry.ID); err != nil {
				if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
					return out, err
				}
				entry = DeltaEntry{ID: entry.ID, Removed: &DeltaRemoved{Reason: "changed"}}
			}
		}
		out.Data = append(out.Data, entry)
	}
	if end < len(entries) {
		out.NextToken = base64.RawURLEncoding.EncodeToString([]byte(token.ID + "." + strconv.Itoa(end)))
	} else {
		out.DeltaToken = token.ID
	}
	return out, nil
}

func (s *Service) CollectExpiredDeltas(ctx context.Context, now time.Time) error {
	now = now.UTC()
	return s.write(ctx, func(t *Service) error {
		if _, err := t.Client.SyncToken.Delete().Where(synctoken.ExpiresAtLTE(now)).Exec(ctx); err != nil {
			return err
		}
		if err := t.Client.SyncToken.Update().Where(synctoken.PageExpiresAtLTE(now)).SetChanges(json.RawMessage(`[]`)).Exec(ctx); err != nil {
			return err
		}
		// A closed range is retained only while an unexpired token can name it.
		if _, err := t.Client.ExecContext(ctx, "DELETE FROM sync_states WHERE to_sequence IS NOT NULL AND NOT EXISTS(SELECT 1 FROM sync_tokens t WHERE t.view_id=sync_states.view_id AND t.sequence>=sync_states.from_sequence AND t.sequence<sync_states.to_sequence)"); err != nil {
			return err
		}
		if _, err := t.Client.ExecContext(ctx, "DELETE FROM sync_states WHERE view_id IN (SELECT v.id FROM sync_views v WHERE NOT EXISTS(SELECT 1 FROM sync_tokens t WHERE t.view_id=v.id))"); err != nil {
			return err
		}
		_, err := t.Client.ExecContext(ctx, "DELETE FROM sync_views WHERE NOT EXISTS(SELECT 1 FROM sync_tokens t WHERE t.view_id=sync_views.id)")
		return err
	})
}

func sortDelta(entries []DeltaEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}

func (s *Service) RunDeltaCleanup(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := s.CollectExpiredDeltas(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
			log.Error("delta cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
