package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

const RESTTokenPrefix = "pg_rest_"

var ErrInvalidRESTToken = errors.New("invalid REST token")

type RESTToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
type RESTTokenInput struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays *int     `json:"expires_in_days,omitempty"`
}
type CreatedRESTToken struct {
	Token  RESTToken `json:"token"`
	Secret string    `json:"secret"`
}
type RESTTokenIdentity struct {
	Subject string
	ID      string
	Scopes  []string
}
type restTokenContextKey struct{}

func WithRESTToken(ctx context.Context, token RESTTokenIdentity) context.Context {
	return context.WithValue(ctx, restTokenContextKey{}, token)
}
func credentialAllows(ctx context.Context, scope string) bool {
	token, limited := ctx.Value(restTokenContextKey{}).(RESTTokenIdentity)
	if !limited {
		return true
	}
	for _, allowed := range token.Scopes {
		if allowed == scope {
			return true
		}
	}
	return false
}
func tokenHash(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}
func (d Directory) CreateRESTToken(ctx context.Context, subject string, in RESTTokenInput) (CreatedRESTToken, error) {
	var out CreatedRESTToken
	in.Name = strings.TrimSpace(in.Name)
	if err := validateName(in.Name, ""); err != nil {
		return out, err
	}
	if len(in.Scopes) == 0 || len(in.Scopes) > len(Catalog) {
		return out, invalid("provide at least one scope from the scope catalog")
	}
	days := 90
	if in.ExpiresInDays != nil {
		days = *in.ExpiresInDays
	}
	if days < 1 || days > 3650 {
		return out, invalid("expires_in_days must be 1..3650")
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireActive(ctx, tx, subject); err != nil {
			return err
		}
		granted, err := EffectiveScopes(ctx, tx, subject)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, scope := range granted {
			allowed[scope] = true
		}
		seen := map[string]bool{}
		for _, scope := range in.Scopes {
			if !allowed[scope] || seen[scope] {
				return invalid("token scopes must be distinct scopes the caller currently holds")
			}
			seen[scope] = true
		}
		var count int
		now := time.Now().UTC()
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM identity_rest_tokens WHERE subject=? AND revoked_at IS NULL AND expires_at>?", subject, now).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return invalid("at most 100 active REST tokens per user")
		}
		secretBytes := make([]byte, 32)
		if _, err = rand.Read(secretBytes); err != nil {
			return err
		}
		out.Secret = RESTTokenPrefix + base64.RawURLEncoding.EncodeToString(secretBytes)
		out.Token = RESTToken{ID: uuid.NewString(), Name: in.Name, Prefix: out.Secret[:len(RESTTokenPrefix)+8], Scopes: append([]string{}, in.Scopes...), CreatedAt: now, ExpiresAt: now.AddDate(0, 0, days)}
		raw, _ := json.Marshal(in.Scopes)
		_, err = tx.ExecContext(ctx, "INSERT INTO identity_rest_tokens(id,created_at,subject,name,prefix,secret_hash,scopes,expires_at) VALUES(?,?,?,?,?,?,?,?)", out.Token.ID, now, subject, in.Name, out.Token.Prefix, tokenHash(out.Secret), string(raw), out.Token.ExpiresAt)
		return err
	})
	if err != nil {
		return CreatedRESTToken{}, err
	}
	return out, nil
}
func (d Directory) RESTTokens(ctx context.Context, subject string) ([]RESTToken, error) {
	if err := RequireActive(ctx, d.DB, subject); err != nil {
		return nil, err
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT id,name,prefix,scopes,created_at,expires_at,last_used_at FROM identity_rest_tokens WHERE subject=? AND revoked_at IS NULL ORDER BY id", subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RESTToken{}
	for rows.Next() {
		var token RESTToken
		var raw string
		var used sql.NullTime
		if err = rows.Scan(&token.ID, &token.Name, &token.Prefix, &raw, &token.CreatedAt, &token.ExpiresAt, &used); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &token.Scopes); err != nil {
			return nil, err
		}
		if used.Valid {
			token.LastUsedAt = &used.Time
		}
		out = append(out, token)
	}
	return out, rows.Err()
}
func (d Directory) RevokeRESTToken(ctx context.Context, subject, id string) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireActive(ctx, tx, subject); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "UPDATE identity_rest_tokens SET revoked_at=? WHERE id=? AND subject=? AND revoked_at IS NULL", time.Now().UTC(), id, subject)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count == 0 {
			return ErrNotFound
		}
		return err
	})
}
func (d Directory) AuthenticateRESTToken(ctx context.Context, secret string) (RESTTokenIdentity, error) {
	var out RESTTokenIdentity
	if !strings.HasPrefix(secret, RESTTokenPrefix) || len(secret) != len(RESTTokenPrefix)+43 {
		return out, ErrInvalidRESTToken
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT t.id,t.subject,t.scopes FROM identity_rest_tokens t JOIN identity_users u ON u.id=t.subject WHERE t.secret_hash=? AND t.revoked_at IS NULL AND t.expires_at>? AND u.active=1 AND u.deleted_at IS NULL", tokenHash(secret), now).Scan(&out.ID, &out.Subject, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRESTToken
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &out.Scopes); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE identity_rest_tokens SET last_used_at=? WHERE id=?", now, out.ID)
		return err
	})
	return out, err
}
