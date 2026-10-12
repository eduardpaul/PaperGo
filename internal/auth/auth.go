package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"papergo/internal/config"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Identity struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	Groups            []string
	SyncGroups        bool
}

type Kind string

const (
	Bearer       Kind = "bearer"
	TrustedProxy Kind = "proxy"
)

type Authenticator interface {
	Authenticate(*http.Request) (Identity, error)
	Kind() Kind
}

type tokenVerifier interface {
	Verify(context.Context, string) (Identity, error)
}

type Development struct {
	Token             string
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	Groups            []string
	SyncGroups        bool
}

func (d Development) Verify(_ context.Context, token string) (Identity, error) {
	if subtle.ConstantTimeCompare([]byte(d.Token), []byte(token)) != 1 || d.Token == "" {
		return Identity{}, errors.New("invalid token")
	}
	return Identity{
		Subject:           d.Subject,
		Name:              d.Name,
		PreferredUsername: d.PreferredUsername,
		Email:             d.Email,
		Groups:            d.Groups,
		SyncGroups:        d.SyncGroups || len(d.Groups) > 0,
	}, nil
}

var ErrUnauthenticated = errors.New("valid authentication is required")

// authenticateBearer verifies a provider bearer token and returns its identity.
// REST also recognizes locally issued personal credentials in its middleware.
func authenticateBearer(r *http.Request, v tokenVerifier) (Identity, error) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 8192 {
		return Identity{}, ErrUnauthenticated
	}
	ident, err := v.Verify(r.Context(), parts[1])
	if err != nil || ident.Subject == "" {
		return Identity{}, ErrUnauthenticated
	}
	return ident, nil
}

type userInfoCacheEntry struct {
	groups     []string
	syncGroups bool
	profile    struct {
		name              string
		preferredUsername string
		email             string
	}
	exp time.Time
}

const maxUserInfoCacheEntries = 1024

type OIDC struct {
	verifier           *oidc.IDTokenVerifier
	provider           *oidc.Provider
	groupsClaim        string
	groupsPrefix       string
	expectedTokenType  string
	groupsFromUserInfo bool
	cacheMu            sync.RWMutex
	userInfoCache      map[string]userInfoCacheEntry
}

func (o *OIDC) cacheUserInfo(raw string, entry userInfoCacheEntry) {
	o.cacheMu.Lock()
	defer o.cacheMu.Unlock()
	now := time.Now()
	var earliestToken string
	var earliestExpiry time.Time
	for token, cached := range o.userInfoCache {
		if !now.Before(cached.exp) {
			delete(o.userInfoCache, token)
			continue
		}
		if earliestExpiry.IsZero() || cached.exp.Before(earliestExpiry) {
			earliestToken, earliestExpiry = token, cached.exp
		}
	}
	if _, exists := o.userInfoCache[raw]; !exists && len(o.userInfoCache) >= maxUserInfoCacheEntries {
		delete(o.userInfoCache, earliestToken)
	}
	o.userInfoCache[raw] = entry
}

func (o *OIDC) Verify(ctx context.Context, raw string) (Identity, error) {
	if o.expectedTokenType != "" {
		parts := strings.Split(raw, ".")
		if len(parts) != 3 {
			return Identity{}, errors.New("invalid token structure")
		}
		headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			return Identity{}, errors.New("invalid token header encoding")
		}
		var header struct {
			Typ string `json:"typ"`
		}
		if err = json.Unmarshal(headerBytes, &header); err != nil {
			return Identity{}, errors.New("invalid token header JSON")
		}
		wanted := o.expectedTokenType
		if !strings.EqualFold(header.Typ, wanted) && !strings.EqualFold(header.Typ, "application/"+wanted) {
			return Identity{}, fmt.Errorf("invalid token type: expected %s, got %s", wanted, header.Typ)
		}
	}
	token, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	if token.Subject == "" || len(token.Subject) > 255 {
		return Identity{}, errors.New("invalid token subject")
	}

	var claims map[string]any
	if err = token.Claims(&claims); err != nil {
		return Identity{}, err
	}

	ident := Identity{
		Subject: token.Subject,
	}
	if v, ok := claims["name"].(string); ok {
		ident.Name = v
	}
	if v, ok := claims["preferred_username"].(string); ok {
		ident.PreferredUsername = v
	}
	if v, ok := claims["email"].(string); ok {
		ident.Email = v
	}

	if o.groupsClaim != "" {
		rawGroups, found := LookupClaim(claims, o.groupsClaim)
		if found {
			ident.Groups = ParseGroups(rawGroups, o.groupsPrefix)
			ident.SyncGroups = true
		} else if o.groupsFromUserInfo && o.provider != nil {
			o.cacheMu.RLock()
			cached, ok := o.userInfoCache[raw]
			o.cacheMu.RUnlock()
			if ok && time.Now().Before(cached.exp) {
				ident.Groups = cached.groups
				ident.SyncGroups = cached.syncGroups
				if ident.Name == "" {
					ident.Name = cached.profile.name
				}
				if ident.PreferredUsername == "" {
					ident.PreferredUsername = cached.profile.preferredUsername
				}
				if ident.Email == "" {
					ident.Email = cached.profile.email
				}
			} else {
				ui, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: raw}))
				if err == nil {
					var uiClaims map[string]any
					if err = ui.Claims(&uiClaims); err == nil {
						rawUIGroups, uiFound := LookupClaim(uiClaims, o.groupsClaim)
						if uiFound {
							ident.Groups = ParseGroups(rawUIGroups, o.groupsPrefix)
							ident.SyncGroups = true
						}
						var entry userInfoCacheEntry
						entry.groups = ident.Groups
						entry.syncGroups = ident.SyncGroups
						if ident.Name == "" {
							if v, ok := uiClaims["name"].(string); ok {
								ident.Name = v
							}
						}
						if ident.PreferredUsername == "" {
							if v, ok := uiClaims["preferred_username"].(string); ok {
								ident.PreferredUsername = v
							}
						}
						if ident.Email == "" {
							if v, ok := uiClaims["email"].(string); ok {
								ident.Email = v
							}
						}
						entry.profile.name = ident.Name
						entry.profile.preferredUsername = ident.PreferredUsername
						entry.profile.email = ident.Email
						entry.exp = token.Expiry
						if entry.exp.IsZero() {
							entry.exp = time.Now().Add(10 * time.Minute)
						}
						o.cacheUserInfo(raw, entry)
					}
				}
			}
		}
	}

	return ident, nil
}

func LookupClaim(claims map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = claims
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := m[part]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}

func ParseGroups(raw any, prefix string) []string {
	var rawList []string
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				rawList = append(rawList, s)
			}
		}
	case []string:
		rawList = append(rawList, v...)
	case string:
		for _, item := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				rawList = append(rawList, trimmed)
			}
		}
	}
	var out []string
	seen := make(map[string]bool)
	for _, g := range rawList {
		if prefix != "" {
			if !strings.HasPrefix(g, prefix) {
				continue
			}
			g = strings.TrimPrefix(g, prefix)
		}
		if g == "" || g != strings.TrimSpace(g) || len(g) > 200 || !utf8.ValidString(g) {
			continue
		}
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
		if len(out) >= 100 {
			break
		}
	}
	return out
}

func (d Development) Authenticate(r *http.Request) (Identity, error) { return authenticateBearer(r, d) }
func (Development) Kind() Kind                                       { return Bearer }
func (o *OIDC) Authenticate(r *http.Request) (Identity, error)       { return authenticateBearer(r, o) }
func (*OIDC) Kind() Kind                                             { return Bearer }

func New(ctx context.Context, c config.Config) (Authenticator, error) {
	if c.AuthMode == "proxy" {
		if c.PublicURL == nil {
			return nil, errors.New("proxy authentication needs PUBLIC_URL")
		}
		return NewProxy(c.ProxySecret, c.ProxyTrustedCIDRs, c.PublicURL.String())
	}
	if c.AuthMode == "development" {
		return Development{Token: c.DevToken, Subject: c.DevSubject}, nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	return &OIDC{
		verifier:           provider.Verifier(&oidc.Config{ClientID: c.Audience}),
		provider:           provider,
		groupsClaim:        c.GroupsClaim,
		groupsPrefix:       c.GroupsPrefix,
		expectedTokenType:  c.TokenType,
		groupsFromUserInfo: c.GroupsFromUserInfo,
		userInfoCache:      make(map[string]userInfoCacheEntry),
	}, nil
}
