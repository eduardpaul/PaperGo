package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"papergo/internal/dal"
	"papergo/internal/dal/access"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"slices"
	"strings"
	"time"
)

const AccessTokenPrefix = "pgo_"

var ErrInvalidAccessToken = errors.New("invalid access token")

// MaxTokenResources bounds the places one token may be limited to.
const MaxTokenResources = 50

// LimitableScopes are the scopes a token limited to some places may hold:
// reading and changing content, and the organization catalogs content needs.
// Scopes that act across the organization stay with unlimited tokens.
var LimitableScopes = []string{"workspace.read", "item.read", "item.write", "comment.write", "document.read", "document.write", "contentType.read", "taxonomy.read", "user.read", "group.read"}

type AccessToken struct {
	Subject     string     `json:"subject"`
	Integration bool       `json:"integration"`
	CreatedBy   string     `json:"created_by"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Scopes      []string   `json:"scopes"`
	// Resources limits the token to these workspaces, lists and libraries;
	// empty is unlimited.
	Resources  []string   `json:"resources"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
type AccessTokenInput struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// Resources, when given, limits the token to these workspaces, lists and
	// libraries.
	Resources     []string `json:"resources,omitempty"`
	ExpiresInDays *int     `json:"expires_in_days,omitempty"`
}
type CreatedAccessToken struct {
	Token  AccessToken `json:"token"`
	Secret string      `json:"secret"`
}
type AccessTokenIdentity struct {
	Integration bool
	Subject     string
	ID          string
	Scopes      []string
	Resources   []string
}

// Credential is the token as authorization checks see it.
func (t AccessTokenIdentity) Credential() *access.Credential {
	return &access.Credential{ID: t.ID, Scopes: t.Scopes, Resources: t.Resources}
}

type accessTokenContextKey struct{}

// WithAccessToken runs ctx with an access token: its scopes and resource
// limit cap what the request may do, and its audit events name it.
func WithAccessToken(ctx context.Context, token AccessTokenIdentity) context.Context {
	return audit.WithCredential(context.WithValue(ctx, accessTokenContextKey{}, token), token.ID)
}

// WithoutAccessToken runs ctx without the request's access token, so a
// check of what another person may do sees their own access rather than
// the token's limits.
func WithoutAccessToken(ctx context.Context) context.Context {
	return context.WithValue(ctx, accessTokenContextKey{}, nil)
}

func AccessTokenContext(ctx context.Context) (AccessTokenIdentity, bool) {
	token, limited := ctx.Value(accessTokenContextKey{}).(AccessTokenIdentity)
	return token, limited
}

// RestoreCredential runs queued work with the access token that started it:
// the scopes it recorded and the live token's resource limit, after
// checking the token still works.
func RestoreCredential(ctx context.Context, store *dal.Store, subject, id string, scopes []string) (context.Context, error) {
	ctx = WithAccessToken(ctx, AccessTokenIdentity{Subject: subject, ID: id, Scopes: scopes})
	if err := RequireActive(ctx, store, subject); err != nil {
		return ctx, err
	}
	tokens, err := store.Directory().AccessTokens(ctx, directory.AccessTokenFilter{ID: id, Subject: subject, Live: true, Now: time.Now().UTC()})
	if err != nil {
		return ctx, err
	}
	if len(tokens) == 0 {
		return ctx, ErrInvalidAccessToken
	}
	return WithAccessToken(ctx, AccessTokenIdentity{Subject: subject, ID: id, Scopes: scopes, Integration: tokens[0].Integration, Resources: tokens[0].Resources}), nil
}

// Revalidate long-lived requests and queued work without retaining the secret.
func RequireCredential(ctx context.Context, store *dal.Store, subject string) error {
	if err := RequireActive(ctx, store, subject); err != nil {
		return err
	}
	if token, limited := AccessTokenContext(ctx); limited {
		tokens, err := store.Directory().AccessTokens(ctx, directory.AccessTokenFilter{ID: token.ID, Subject: subject, Live: true, Now: time.Now().UTC()})
		if err != nil {
			return err
		}
		valid := len(tokens) > 0
		if !valid {
			return ErrInvalidAccessToken
		}
	}
	return nil
}

// CredentialAllows reports whether the request's access token, if any,
// holds scope; a request without one is not narrowed.
func CredentialAllows(ctx context.Context, scope string) bool { return credentialAllows(ctx, scope) }

func credentialAllows(ctx context.Context, scope string) bool {
	token, limited := ctx.Value(accessTokenContextKey{}).(AccessTokenIdentity)
	if !limited {
		return true
	}
	if len(token.Resources) > 0 && !slices.Contains(LimitableScopes, scope) {
		return false
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
func (d Directory) CreateAccessToken(ctx context.Context, subject string, in AccessTokenInput) (CreatedAccessToken, error) {
	return d.createAccessToken(ctx, subject, in, false)
}

func (d Directory) CreateIntegrationToken(ctx context.Context, actor string, in AccessTokenInput) (CreatedAccessToken, error) {
	return d.createAccessToken(ctx, actor, in, true)
}

func (d Directory) createAccessToken(ctx context.Context, actor string, in AccessTokenInput, integration bool) (CreatedAccessToken, error) {
	var out CreatedAccessToken
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
	if err := validateLimit(in); err != nil {
		return out, err
	}
	// A token limited to some places cannot issue tokens, which could reach
	// more.
	if token, ok := AccessTokenContext(ctx); ok && len(token.Resources) > 0 {
		return out, ErrForbidden
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityAccessToken, directory.ChangeCreate, tokenID, func() error {
			if err := RequireActive(ctx, d.store(tx), actor); err != nil {
				return err
			}
			if integration {
				if err := RequireScope(ctx, d.store(tx), actor, "organization.manage"); err != nil {
					return err
				}
			} else {
				user, err := d.store(tx).Directory().User(ctx, subject)
				if err != nil {
					return err
				}
				if user.Service {
					return ErrForbidden
				}
			}
			granted, err := EffectiveScopes(ctx, d.store(tx), actor)
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
			if len(in.Resources) > 0 {
				readable, err := d.store(tx).Resources().IDs(ctx, resources.Filter{IDs: in.Resources, Kinds: []refs.ResourceKind{refs.KindWorkspace, refs.KindList, refs.KindLibrary},
					Reader: &resources.Reader{Principal: access.Principal{Subject: actor}, Action: access.Read}})
				if err != nil {
					return err
				}
				if len(readable) != len(in.Resources) {
					return invalid("resources must be workspaces, lists or libraries the caller can read")
				}
			}
			now := time.Now().UTC()
			if integration {
				if _, err = d.store(tx).Directory().CreateUser(ctx, subject, in.Name, "", true, true); err != nil {
					return err
				}
			}
			tokens, err := d.store(tx).Directory().AccessTokens(ctx, directory.AccessTokenFilter{Subject: subject, Live: true, Now: now})
			if err != nil {
				return err
			}
			count := len(tokens)
			if count >= 100 {
				return invalid("at most 100 active access tokens per user")
			}
			secretBytes := make([]byte, 32)
			if _, err = rand.Read(secretBytes); err != nil {
				return err
			}
			out.Secret = AccessTokenPrefix + base64.RawURLEncoding.EncodeToString(secretBytes)
			var expiry *time.Time
			if days != 0 {
				value := now.AddDate(0, 0, days)
				expiry = &value
			}
			out.Token = AccessToken{Subject: subject, Integration: integration, CreatedBy: actor, ID: tokenID, Name: in.Name, Prefix: out.Secret[:len(AccessTokenPrefix)+8], Scopes: append([]string{}, in.Scopes...),
				Resources: append([]string{}, in.Resources...), CreatedAt: now, ExpiresAt: expiry}
			_, err = d.store(tx).Directory().CreateAccessToken(ctx, directory.AccessToken{ID: tokenID, Subject: subject, Name: in.Name, Prefix: out.Token.Prefix, SecretHash: tokenHash(out.Secret), Scopes: in.Scopes,
				Resources: out.Token.Resources, Integration: integration, CreatedBy: actor, CreatedAt: now, ExpiresAt: expiry})
			return err
		})
	})
	if err != nil {
		return CreatedAccessToken{}, err
	}
	return out, nil
}
func (d Directory) AccessTokens(ctx context.Context, subject string) ([]AccessToken, error) {
	if err := RequireActive(ctx, dal.NewStore(d.DB), subject); err != nil {
		return nil, err
	}
	return d.accessTokens(ctx, directory.AccessTokenFilter{Subject: subject, Personal: true})
}
func (d Directory) OrganizationAccessTokens(ctx context.Context, actor string) ([]AccessToken, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "organization.manage"); err != nil {
		return nil, err
	}
	return d.accessTokens(ctx, directory.AccessTokenFilter{})
}
func tokenRecord(t directory.AccessToken) AccessToken {
	return AccessToken{ID: t.ID, Subject: t.Subject, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes, Resources: t.Resources, Integration: t.Integration, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}
func (d Directory) accessTokens(ctx context.Context, filter directory.AccessTokenFilter) ([]AccessToken, error) {
	tokens, err := dal.NewStore(d.DB).Directory().AccessTokens(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := []AccessToken{}
	for _, t := range tokens {
		if filter.Personal && t.RevokedAt != nil {
			continue
		}
		out = append(out, tokenRecord(t))
	}
	return out, nil
}
func (d Directory) RevokeAccessToken(ctx context.Context, subject, id string) error {
	return d.revokeAccessToken(ctx, subject, id, false)
}
func (d Directory) RevokeOrganizationAccessToken(ctx context.Context, actor, id string) error {
	return d.revokeAccessToken(ctx, actor, id, true)
}
func (d Directory) revokeAccessToken(ctx context.Context, subject, id string, admin bool) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), subject, directory.EntityAccessToken, directory.ChangeRevoke, id, func() error {
			if err := RequireActive(ctx, d.store(tx), subject); err != nil {
				return err
			}
			if admin {
				if err := RequireScope(ctx, d.store(tx), subject, "organization.manage"); err != nil {
					return err
				}
			}
			return identityError(d.store(tx).Directory().RevokeAccessToken(ctx, id, subject, admin))
		})
	})
}
func (d Directory) AuthenticateAccessToken(ctx context.Context, secret string) (AccessTokenIdentity, error) {
	var out AccessTokenIdentity
	if !strings.HasPrefix(secret, AccessTokenPrefix) || len(secret) != len(AccessTokenPrefix)+43 {
		return out, ErrInvalidAccessToken
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		repo := d.store(tx).Directory()
		tokens, err := repo.AccessTokens(ctx, directory.AccessTokenFilter{SecretHash: tokenHash(secret), Live: true, Now: now})
		if err != nil {
			return err
		}
		if len(tokens) != 1 {
			return ErrInvalidAccessToken
		}
		t := tokens[0]
		if err := RequireActive(ctx, d.store(tx), t.Subject); err != nil {
			if errors.Is(err, ErrForbidden) {
				return ErrInvalidAccessToken
			}
			return err
		}
		out = AccessTokenIdentity{ID: t.ID, Subject: t.Subject, Integration: t.Integration, Scopes: t.Scopes, Resources: t.Resources}
		return repo.TouchAccessToken(ctx, t.ID, now)
	})
	return out, err
}

// validateLimit checks a token's resource limit: distinct IDs, at most
// MaxTokenResources, and only scopes a limited token may hold.
func validateLimit(in AccessTokenInput) error {
	if len(in.Resources) == 0 {
		if in.Resources != nil {
			return invalid("resources, when given, must name at least one workspace, list or library")
		}
		return nil
	}
	if len(in.Resources) > MaxTokenResources {
		return invalid("a token can be limited to at most 50 workspaces, lists and libraries")
	}
	seen := map[string]bool{}
	for _, id := range in.Resources {
		if id == "" || seen[id] {
			return invalid("resources must be distinct workspace, list or library IDs")
		}
		seen[id] = true
	}
	for _, scope := range in.Scopes {
		if !slices.Contains(LimitableScopes, scope) {
			return invalid("a token limited to resources can hold only " + strings.Join(LimitableScopes, ", ") + "; " + scope + " acts across the organization")
		}
	}
	return nil
}
