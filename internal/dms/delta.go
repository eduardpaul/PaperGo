package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/delta"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrDeltaExpired = errors.New("delta token has expired; synchronize without a token")

const (
	// deltaPageTTL is how long a token keeps the page of changes it returned.
	deltaPageTTL = 5 * time.Minute
	// deltaTokenTTL is how long an unused token names its snapshot.
	deltaTokenTTL = 30 * 24 * time.Hour
)

type DeltaRemoved struct {
	Reason string `json:"reason"`
}
type DeltaEntry struct {
	*resources.Resource
	ID      string        `json:"id"`
	Removed *DeltaRemoved `json:"@removed,omitempty"`
}
type DeltaResult struct {
	Data       []DeltaEntry
	NextToken  string
	DeltaToken string
}
type visibleDeltaItem struct {
	item        *resources.Resource
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
		var anchor *delta.Token
		var view *delta.View
		if deltaToken != "" {
			token, v, err := t.deltaAnchor(ctx, subject, collectionID, deltaToken, now)
			if err != nil {
				return err
			}
			anchor, view = &token, &v
		} else {
			v, err := t.DAL.Delta().View(ctx, subject, collectionID)
			if err == nil {
				view = &v
			} else if !dberr.IsNotFound(err) {
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
			v, err := t.DAL.Delta().CreateView(ctx, subject, collectionID)
			if err != nil {
				return err
			}
			view = &v
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
			var ended []string
			for id, fingerprint := range latest {
				if item, exists := current[id]; !exists || item.fingerprint != fingerprint {
					ended = append(ended, id)
				}
			}
			started := map[string]string{}
			for id, item := range current {
				if latest[id] != item.fingerprint {
					started[id] = item.fingerprint
				}
			}
			next, err := t.DAL.Delta().Advance(ctx, *view, ended, started)
			if err != nil {
				return err
			}
			view = &next
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
			row, e := t.DAL.Resources().Resource(ctx, id)
			if dberr.IsNotFound(e) || e == nil && row.DeletedAt != nil {
				reason = "deleted"
			} else if e != nil {
				return e
			}
			entries = append(entries, DeltaEntry{ID: id, Removed: &DeltaRemoved{Reason: reason}})
		}
		sortDelta(entries)
		if anchor != nil && len(entries) == 0 && anchor.Sequence == view.Sequence {
			if err = t.DAL.Delta().Renew(ctx, anchor.ID, now.Add(deltaTokenTTL)); err != nil {
				return err
			}
			out = DeltaResult{Data: []DeltaEntry{}, DeltaToken: anchor.ID}
			return nil
		}
		payload, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		token, err := t.DAL.Delta().CreateToken(ctx, *view, payload, deltaPageTTL, deltaTokenTTL)
		if err != nil {
			return err
		}
		out, err = t.deltaPage(ctx, subject, token, entries, 0, limit)
		return err
	})
	return out, err
}
func (s *Service) deltaAnchor(ctx context.Context, subject, collectionID, id string, now time.Time) (delta.Token, delta.View, error) {
	if _, err := uuid.Parse(id); err != nil {
		return delta.Token{}, delta.View{}, invalid("invalid delta token")
	}
	token, err := s.DAL.Delta().Token(ctx, id)
	if dberr.IsNotFound(err) || err == nil && !token.ExpiresAt.After(now) {
		return delta.Token{}, delta.View{}, ErrDeltaExpired
	}
	if err != nil {
		return delta.Token{}, delta.View{}, err
	}
	view, err := s.DAL.Delta().ViewByID(ctx, token.ViewID)
	if err != nil {
		return delta.Token{}, delta.View{}, err
	}
	if view.Subject != subject || view.ContainerID != collectionID {
		return delta.Token{}, delta.View{}, ErrDeltaExpired
	}
	return token, view, nil
}
func (s *Service) deltaState(ctx context.Context, viewID string, sequence int64) (map[string]string, error) {
	return s.DAL.Delta().States(ctx, viewID, sequence)
}
func (s *Service) deltaVisible(ctx context.Context, subject, collectionID string) (map[string]visibleDeltaItem, error) {
	if err := s.RequireScope(ctx, subject, "item.read"); err != nil {
		return nil, err
	}
	c, err := s.authorize(ctx, subject, collectionID, "read")
	if err != nil {
		return nil, err
	}
	if c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
		return nil, invalid("delta requires a list or library")
	}
	out := map[string]visibleDeltaItem{}
	after := ""
	for {
		rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{ContainerID: collectionID, Kinds: ContentKinds, State: resources.Live, AfterID: after,
			Reader: &resources.Reader{Principal: principal(ctx, subject), Action: access.Read, Surface: "auto"}, Limit: 500})
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
func (s *Service) deltaPage(ctx context.Context, subject string, token delta.Token, entries []DeltaEntry, offset, limit int) (DeltaResult, error) {
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
	return s.write(ctx, func(t *Service) error { return t.DAL.Delta().CollectExpired(ctx, now) })
}

func sortDelta(entries []DeltaEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}
