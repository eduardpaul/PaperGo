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

const RESTTokenPrefix = "pgo_"

var ErrInvalidRESTToken = errors.New("invalid REST token")

type RESTToken struct {
	Subject     string     `json:"subject"`
	Integration bool       `json:"integration"`
	CreatedBy   string     `json:"created_by"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
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
	Integration bool
	Subject     string
	ID          string
	Scopes      []string
}
type restTokenContextKey struct{}

func WithRESTToken(ctx context.Context, token RESTTokenIdentity) context.Context {
	return context.WithValue(ctx, restTokenContextKey{}, token)
}

func RESTTokenContext(ctx context.Context) (RESTTokenIdentity, bool) {
	token, limited := ctx.Value(restTokenContextKey{}).(RESTTokenIdentity)
	return token, limited
}

// Revalidate long-lived requests and queued work without retaining the secret.
func RequireCredential(ctx context.Context, q Querier, subject string) error {
	if err := RequireActive(ctx, q, subject); err != nil {
		return err
	}
	if token, limited := RESTTokenContext(ctx); limited {
		var valid bool
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_rest_tokens WHERE id=? AND subject=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?))", token.ID, subject, time.Now().UTC()).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrInvalidRESTToken
		}
	}
	return nil
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
	return d.createRESTToken(ctx, subject, in, false)
}

func (d Directory) CreateIntegrationToken(ctx context.Context, actor string, in RESTTokenInput) (CreatedRESTToken, error) {
	return d.createRESTToken(ctx, actor, in, true)
}

func (d Directory) createRESTToken(ctx context.Context, actor string, in RESTTokenInput, integration bool) (CreatedRESTToken, error) {
	var out CreatedRESTToken
	subject := actor
	if integration {
		subject = "service:" + uuid.NewString()
	}
	tokenID := uuid.NewString()
	in.Name = strings.TrimSpace(in.Name)
	if err := ValidateName(in.Name, ""); err != nil {
		return out, err
	}
	if len(in.Scopes) == 0 || len(in.Scopes) > len(Catalog) {
		return out, invalid("provide at least one scope from the scope catalog")
	}
	days := 90
	if in.ExpiresInDays != nil {
		days = *in.ExpiresInDays
	}
	if days < 0 || days > 3650 || (!integration && days == 0) {
		return out, invalid("expires_in_days must be 1..3650; integration tokens also accept 0 for no expiration")
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "rest_token.create", "rest_token", tokenID, func() error {
			if err := RequireActive(ctx, tx, actor); err != nil {
				return err
			}
			if integration {
				if err := RequireScope(ctx, tx, actor, "organization.manage"); err != nil {
					return err
				}
			} else {
				var service bool
				if err := tx.QueryRowContext(ctx, "SELECT service FROM identity_users WHERE id=?", subject).Scan(&service); err != nil {
					return err
				}
				if service {
					return ErrForbidden
				}
			}
			granted, err := EffectiveScopes(ctx, tx, actor)
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
			if integration {
				if _, err = tx.ExecContext(ctx, "INSERT INTO identity_users(id,created_at,display_name,service) VALUES(?,?,?,1)", subject, now, in.Name); err != nil {
					return err
				}
			}
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM identity_rest_tokens WHERE subject=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)", subject, now).Scan(&count); err != nil {
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
			var expiry *time.Time
			if days != 0 {
				value := now.AddDate(0, 0, days)
				expiry = &value
			}
			out.Token = RESTToken{Subject: subject, Integration: integration, CreatedBy: actor, ID: tokenID, Name: in.Name, Prefix: out.Secret[:len(RESTTokenPrefix)+8], Scopes: append([]string{}, in.Scopes...), CreatedAt: now, ExpiresAt: expiry}
			raw, _ := json.Marshal(in.Scopes)
			_, err = tx.ExecContext(ctx, "INSERT INTO identity_rest_tokens(id,created_at,subject,name,prefix,secret_hash,scopes,expires_at,integration,created_by) VALUES(?,?,?,?,?,?,?,?,?,?)", out.Token.ID, now, subject, in.Name, out.Token.Prefix, tokenHash(out.Secret), string(raw), out.Token.ExpiresAt, integration, actor)
			return err
		})
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
	return d.restTokens(ctx, "subject=? AND integration=0 AND revoked_at IS NULL", subject)
}
func (d Directory) OrganizationRESTTokens(ctx context.Context, actor string) ([]RESTToken, error) {
	if err := RequireScope(ctx, d.DB, actor, "organization.manage"); err != nil {
		return nil, err
	}
	return d.restTokens(ctx, "1=1")
}
func (d Directory) restTokens(ctx context.Context, where string, args ...any) ([]RESTToken, error) {
	rows, err := d.DB.QueryContext(ctx, "SELECT id,name,prefix,scopes,created_at,expires_at,last_used_at,subject,integration,created_by,revoked_at FROM identity_rest_tokens WHERE "+where+" ORDER BY created_at DESC,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RESTToken{}
	for rows.Next() {
		var token RESTToken
		var raw string
		var used, expiry, revoked sql.NullTime
		if err = rows.Scan(&token.ID, &token.Name, &token.Prefix, &raw, &token.CreatedAt, &expiry, &used, &token.Subject, &token.Integration, &token.CreatedBy, &revoked); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &token.Scopes); err != nil {
			return nil, err
		}
		if expiry.Valid {
			token.ExpiresAt = &expiry.Time
		}
		if revoked.Valid {
			token.RevokedAt = &revoked.Time
		}
		if used.Valid {
			token.LastUsedAt = &used.Time
		}
		out = append(out, token)
	}
	return out, rows.Err()
}
func (d Directory) RevokeRESTToken(ctx context.Context, subject, id string) error {
	return d.revokeRESTToken(ctx, subject, id, false)
}
func (d Directory) RevokeOrganizationRESTToken(ctx context.Context, actor, id string) error {
	return d.revokeRESTToken(ctx, actor, id, true)
}
func (d Directory) revokeRESTToken(ctx context.Context, subject, id string, admin bool) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, subject, "rest_token.revoke", "rest_token", id, func() error {
			if err := RequireActive(ctx, tx, subject); err != nil {
				return err
			}
			where := "id=? AND subject=? AND integration=0"
			args := []any{time.Now().UTC(), id, subject}
			if admin {
				if err := RequireScope(ctx, tx, subject, "organization.manage"); err != nil {
					return err
				}
				where, args = "id=?", args[:2]
			}
			result, err := tx.ExecContext(ctx, "UPDATE identity_rest_tokens SET revoked_at=? WHERE "+where+" AND revoked_at IS NULL", args...)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err == nil && count == 0 {
				return ErrNotFound
			}
			return err
		})
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
		err := tx.QueryRowContext(ctx, "SELECT t.id,t.subject,t.scopes,t.integration FROM identity_rest_tokens t JOIN identity_users u ON u.id=t.subject WHERE t.secret_hash=? AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>?) AND u.active=1 AND u.deleted_at IS NULL", tokenHash(secret), now).Scan(&out.ID, &out.Subject, &raw, &out.Integration)
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
