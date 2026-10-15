// Package resultcache stores reusable results in PaperGo's main database.
// Callers own their namespaces, sharing scopes, keys and JSON payloads.
// Entries are immutable until they expire; access extends their lifetime.
package resultcache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const MaxPayloadBytes = 1 << 20

// Cache is available to actions outside domain transactions. A scope is a
// sharing boundary, not an authorization grant: callers authorize inputs first.
type Cache interface {
	Get(ctx context.Context, namespace, scope, key string) (json.RawMessage, error)
	// PutIfAbsent returns the winning payload, including when another caller
	// already saved a live result. Expired entries can be replaced.
	PutIfAbsent(ctx context.Context, namespace, scope, key string, payload json.RawMessage, ttl time.Duration) (json.RawMessage, error)
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

func validAddress(namespace, scope, key string) bool {
	return len(namespace) > 0 && len(namespace) <= 64 && len(scope) > 0 && len(scope) <= 255 && len(key) > 0 && len(key) <= 4096
}

// Get returns nil for missing or expired entries. Hits extend expiry by the
// original TTL, at most once a day (or half the TTL for shorter lifetimes).
func (s *Store) Get(ctx context.Context, namespace, scope, key string) (json.RawMessage, error) {
	if !validAddress(namespace, scope, key) {
		return nil, errors.New("invalid result cache address")
	}
	now := s.now().UTC()
	var payload []byte
	var used, expires time.Time
	err := s.db.QueryRowContext(ctx, `SELECT payload,used_at,expires_at FROM cached_results WHERE namespace=? AND scope=? AND key=? AND expires_at>?`, namespace, scope, key, now).Scan(&payload, &used, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ttl := expires.Sub(used)
	if now.Sub(used) >= min(24*time.Hour, ttl/2) {
		// The observed expiry also fences a concurrent replacement or touch.
		_, err = s.db.ExecContext(ctx, `UPDATE cached_results SET used_at=?,expires_at=? WHERE namespace=? AND scope=? AND key=? AND used_at=? AND expires_at=?`, now, now.Add(ttl), namespace, scope, key, used, expires)
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func (s *Store) PutIfAbsent(ctx context.Context, namespace, scope, key string, payload json.RawMessage, ttl time.Duration) (json.RawMessage, error) {
	if !validAddress(namespace, scope, key) || ttl <= 0 || len(payload) > MaxPayloadBytes || !json.Valid(payload) {
		return nil, errors.New("result cache requires a valid address, JSON within 1 MiB and a positive TTL")
	}
	now := s.now().UTC()
	var winner []byte
	// A single upsert chooses the same winning payload for competing callers.
	// A live entry keeps its payload and lifetime; an expired one starts anew.
	err := s.db.QueryRowContext(ctx, `INSERT INTO cached_results (id,namespace,scope,key,payload,created_at,used_at,expires_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(namespace,scope,key) DO UPDATE SET
		 payload=CASE WHEN cached_results.expires_at<=excluded.used_at THEN excluded.payload ELSE cached_results.payload END,
		 created_at=CASE WHEN cached_results.expires_at<=excluded.used_at THEN excluded.created_at ELSE cached_results.created_at END,
		 used_at=CASE WHEN cached_results.expires_at<=excluded.used_at THEN excluded.used_at ELSE cached_results.used_at END,
		 expires_at=CASE WHEN cached_results.expires_at<=excluded.used_at THEN excluded.expires_at ELSE cached_results.expires_at END
		RETURNING payload`, uuid.NewString(), namespace, scope, key, string(payload), now, now, now.Add(ttl)).Scan(&winner)
	return winner, err
}

// PurgeExpired removes entries of every namespace whose expiry has passed.
func (s *Store) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cached_results WHERE expires_at<=?`, now.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
