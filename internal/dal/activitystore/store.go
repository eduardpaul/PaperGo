// Package activitystore persists extension-owned requests, batches and scheduler
// state. References retain live payloads independently of cache and run TTLs.
package activitystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/operations"
	"papergo/internal/jsonutil"
)

var ErrConflict = errors.New("activity record conflicts with existing state")
var ErrLease = errors.New("activity record lease was lost")

type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Store struct{ q querier }

func New(db *sql.DB) *Store               { return &Store{q: db} }
func NewTx(tx *sql.Tx) *Store             { return &Store{q: tx} }
func (s *Store) WithTx(tx *sql.Tx) *Store { return &Store{q: tx} }

// Write binds a set of payload and reference changes to one transaction.
func (s *Store) Write(ctx context.Context, fn func(*Store) error) error {
	if db, ok := s.q.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = fn(NewTx(tx)); err != nil {
			return err
		}
		return tx.Commit()
	}
	return fn(s)
}

func ID(namespace, scope, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(namespace+"\x00"+scope+"\x00"+key)).String()
}

type Record struct {
	ID, Namespace, Scope, Key, OwnerID, State string
	Payload                                   json.RawMessage
	CreatedAt                                 time.Time
	ExpiresAt                                 *time.Time
	LeaseOwner, LeaseToken                    string
}

func expiryUTC(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	value := at.UTC()
	return &value
}

func valid(r Record) error {
	if len(r.Namespace) < 1 || len(r.Namespace) > 64 || len(r.Scope) < 1 || len(r.Scope) > 255 || len(r.Key) < 1 || len(r.Key) > 128 || len(r.Payload) > 1<<20 || !json.Valid(r.Payload) {
		return errors.New("invalid durable activity record")
	}
	return nil
}
func (s *Store) Put(ctx context.Context, r Record) error {
	if err := valid(r); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.State == "" {
		r.State = "pending"
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO activity_records(id,created_at,updated_at,namespace,scope,key,owner_id,payload,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
 ON CONFLICT(namespace,scope,key) DO UPDATE SET payload=excluded.payload,state=excluded.state,expires_at=excluded.expires_at,updated_at=excluded.updated_at`, ID(r.Namespace, r.Scope, r.Key), now, now, r.Namespace, r.Scope, r.Key, r.OwnerID, string(r.Payload), r.State, expiryUTC(r.ExpiresAt))
	return err
}
func (s *Store) PutImmutable(ctx context.Context, r Record) error {
	if err := valid(r); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.q.ExecContext(ctx, `INSERT INTO activity_records(id,created_at,updated_at,namespace,scope,key,owner_id,payload,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9) ON CONFLICT(namespace,scope,key) DO NOTHING`, ID(r.Namespace, r.Scope, r.Key), now, now, r.Namespace, r.Scope, r.Key, r.OwnerID, string(r.Payload), expiryUTC(r.ExpiresAt))
	if err != nil {
		return err
	}
	var payload string
	if err = s.q.QueryRowContext(ctx, `SELECT payload FROM activity_records WHERE id=$1`, ID(r.Namespace, r.Scope, r.Key)).Scan(&payload); err != nil {
		return err
	}
	if !jsonutil.Equal([]byte(payload), r.Payload) {
		return ErrConflict
	}
	return nil
}
func (s *Store) Get(ctx context.Context, namespace, scope, key string) (*Record, error) {
	var r Record
	var payload string
	var leaseOwner, leaseToken sql.NullString
	var expiry sql.NullTime
	err := s.q.QueryRowContext(ctx, `SELECT id,namespace,scope,key,owner_id,payload,state,created_at,expires_at,lease_owner,lease_token FROM activity_records a WHERE id=$1 AND (expires_at IS NULL OR expires_at>$2 OR EXISTS(SELECT 1 FROM activity_references WHERE record_id=a.id))`, ID(namespace, scope, key), time.Now().UTC()).Scan(&r.ID, &r.Namespace, &r.Scope, &r.Key, &r.OwnerID, &payload, &r.State, &r.CreatedAt, &expiry, &leaseOwner, &leaseToken)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Payload = json.RawMessage(payload)
	if expiry.Valid {
		r.ExpiresAt = &expiry.Time
	}
	r.LeaseOwner, r.LeaseToken = leaseOwner.String, leaseToken.String
	return &r, nil
}
func (s *Store) List(ctx context.Context, namespace, scope, state, after string, limit int) ([]Record, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `SELECT key FROM activity_records WHERE namespace=$1 AND scope=$2 AND state=$3 AND key>$4 AND (expires_at IS NULL OR expires_at>$5 OR EXISTS(SELECT 1 FROM activity_references WHERE record_id=activity_records.id)) ORDER BY key LIMIT $6`, namespace, scope, state, after, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, key := range keys {
		r, err := s.Get(ctx, namespace, scope, key)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

// OwnerKind is what holds a reference, which decides when retention may
// drop it.
type OwnerKind string

const (
	OwnerRecord    OwnerKind = "record"
	OwnerOperation OwnerKind = "operation"
)

func (k OwnerKind) Valid() bool { return k == OwnerRecord || k == OwnerOperation }

// Owner holds references: another activity record or an operation, such
// as a workflow run, by ID.
type Owner struct {
	Kind OwnerKind
	ID   string
}

func RecordOwner(id string) Owner    { return Owner{OwnerRecord, id} }
func OperationOwner(id string) Owner { return Owner{OwnerOperation, id} }

func (o Owner) valid() error {
	if !o.Kind.Valid() || o.ID == "" {
		return errors.New("a reference needs a typed owner")
	}
	return nil
}

func (s *Store) Reference(ctx context.Context, owner Owner, recordID string) error {
	if err := owner.valid(); err != nil {
		return err
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO activity_references(id,created_at,owner_kind,owner_id,record_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_kind,owner_id,record_id) DO NOTHING`, uuid.NewString(), time.Now().UTC(), string(owner.Kind), owner.ID, recordID)
	return err
}

// Unreference releases one payload without affecting the owner's other work.
func (s *Store) Unreference(ctx context.Context, owner Owner, recordID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM activity_references WHERE owner_kind=$1 AND owner_id=$2 AND record_id=$3`, string(owner.Kind), owner.ID, recordID)
	return err
}
func (s *Store) Release(ctx context.Context, owner Owner) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM activity_references WHERE owner_kind=$1 AND owner_id=$2`, string(owner.Kind), owner.ID)
	return err
}
func (s *Store) Claim(ctx context.Context, namespace, scope, key, holder string, ttl time.Duration) (string, bool, error) {
	if ttl <= 0 || holder == "" {
		return "", false, errors.New("claim requires holder and positive lifetime")
	}
	now := time.Now().UTC()
	token := uuid.NewString()
	result, err := s.q.ExecContext(ctx, `UPDATE activity_records SET lease_owner=$1,lease_token=$2,lease_until=$3 WHERE id=$4 AND (lease_owner IS NULL OR lease_owner=$5 OR lease_until<=$6)`, holder, token, now.Add(ttl), ID(namespace, scope, key), holder, now)
	if err != nil {
		return "", false, err
	}
	n, err := result.RowsAffected()
	return token, n == 1, err
}
func (s *Store) CheckLease(ctx context.Context, namespace, scope, key, holder, token string) error {
	var n int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_records WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_until>$4`, ID(namespace, scope, key), holder, token, time.Now().UTC()).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return ErrLease
	}
	return nil
}
func (s *Store) ReleaseLease(ctx context.Context, namespace, scope, key, holder, token string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE activity_records SET lease_owner=NULL,lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_owner=$2 AND lease_token=$3`, ID(namespace, scope, key), holder, token)
	return err
}

// CompareAndSwap atomically advances an extension's intent. Only one replay
// can cross an external side-effect boundary from a given recorded payload.
func (s *Store) CompareAndSwap(ctx context.Context, id string, previous, next json.RawMessage) (bool, error) {
	if !json.Valid(next) || len(next) > 1<<20 {
		return false, errors.New("invalid durable activity payload")
	}
	var stored string
	if err := s.q.QueryRowContext(ctx, `SELECT payload FROM activity_records WHERE id=$1 AND state='pending'`, id).Scan(&stored); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if !jsonutil.Equal([]byte(stored), previous) {
		return false, nil
	}
	result, err := s.q.ExecContext(ctx, `UPDATE activity_records SET payload=$1,updated_at=$2 WHERE id=$3 AND payload=$4 AND state='pending'`, string(next), time.Now().UTC(), id, stored)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ReleaseLeases drops the claims of a holder that ended, such as a run.
func (s *Store) ReleaseLeases(ctx context.Context, holder string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE activity_records SET lease_owner=NULL,lease_token=NULL,lease_until=NULL WHERE lease_owner=$1`, holder)
	return err
}
func (s *Store) Purge(ctx context.Context, now time.Time) error {
	now = now.UTC()
	if db, ok := s.q.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.WithTx(tx).Purge(ctx, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	// Owners release their references when they end. A cancelled worker
	// may still write after that cleanup, so retention also drops the
	// references of owners that ended or no longer exist: ended or deleted
	// operations, and expired or deleted records.
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM activity_references WHERE owner_kind=$1 AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.id=activity_references.owner_id AND o.status=$2)`,
			[]any{string(OwnerOperation), string(operations.StatusPending)}},
		{`DELETE FROM activity_references WHERE owner_kind=$1 AND (owner_id IN (SELECT id FROM activity_records WHERE expires_at<=$2 AND NOT EXISTS(SELECT 1 FROM activity_references WHERE record_id=activity_records.id) AND (lease_until IS NULL OR lease_until<=$3)) OR NOT EXISTS(SELECT 1 FROM activity_records r WHERE r.id=activity_references.owner_id))`,
			[]any{string(OwnerRecord), now, now}},
	} {
		if _, err := s.q.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}

	for {
		res, err := s.q.ExecContext(ctx, `DELETE FROM activity_records WHERE id IN (
			SELECT id FROM activity_records WHERE expires_at<=$1 AND NOT EXISTS(SELECT 1 FROM activity_references WHERE record_id=activity_records.id) AND (lease_until IS NULL OR lease_until<=$2) LIMIT $3)`, now, now, activityPurgePage)
		if err != nil {
			return fmt.Errorf("purge activity records: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil || n < activityPurgePage {
			return err
		}
	}
}

// activityPurgePage bounds one activity-record delete.
const activityPurgePage = 200

// CountExpired counts the records past their expiry that nothing references
// or leases.
func (s *Store) CountExpired(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_records WHERE expires_at<=$1 AND NOT EXISTS(SELECT 1 FROM activity_references WHERE record_id=activity_records.id) AND (lease_until IS NULL OR lease_until<=$2)`, now, now).Scan(&n)
	return n, err
}
