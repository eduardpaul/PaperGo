package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"github.com/go-jose/go-jose/v4"
	"net/http"
	"net/http/httptest"
	"papergo/internal/config"
	"sync/atomic"
	"testing"
	"time"
)

func TestOIDCValidatesSignatureIssuerAudienceAndExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "jwks_uri": server.URL + "/keys", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	verifier, err := New(context.Background(), config.Config{AuthMode: "oidc", Issuer: server.URL, Audience: "papergo"})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, issuer, audience string
		expires                time.Time
		valid                  bool
	}{
		{"valid", server.URL, "papergo", time.Now().Add(time.Hour), true},
		{"wrong issuer", "https://other.example", "papergo", time.Now().Add(time.Hour), false},
		{"wrong audience", server.URL, "another-api", time.Now().Add(time.Hour), false},
		{"expired", server.URL, "papergo", time.Now().Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims, _ := json.Marshal(map[string]any{"iss": tc.issuer, "aud": tc.audience, "sub": "alice", "exp": tc.expires.Unix(), "iat": time.Now().Add(-time.Minute).Unix()})
			signed, e := signer.Sign(claims)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := signed.CompactSerialize()
			if e != nil {
				t.Fatal(e)
			}
			ident, e := verifier.(*OIDC).Verify(context.Background(), raw)
			if tc.valid {
				if e != nil || ident.Subject != "alice" {
					t.Fatalf("valid token rejected: %v", e)
				}
				bytes := []byte(raw)
				if bytes[len(bytes)-5] == 'A' {
					bytes[len(bytes)-5] = 'B'
				} else {
					bytes[len(bytes)-5] = 'A'
				}
				if _, e = verifier.(*OIDC).Verify(context.Background(), string(bytes)); e == nil {
					t.Fatal("tampered signature accepted")
				}
			} else if e == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}

func TestOIDCTokenTypeValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "jwks_uri": server.URL + "/keys", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	verifier, err := New(context.Background(), config.Config{
		AuthMode:  "oidc",
		Issuer:    server.URL,
		Audience:  "papergo",
		TokenType: "at+jwt",
	})
	if err != nil {
		t.Fatal(err)
	}

	signerNormal, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	signerAtJwt, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, (&jose.SignerOptions{}).WithType("at+jwt"))
	if err != nil {
		t.Fatal(err)
	}

	claims, _ := json.Marshal(map[string]any{
		"iss": server.URL,
		"aud": "papergo",
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	})

	// Token without typ header should be rejected
	s1, _ := signerNormal.Sign(claims)
	tok1, _ := s1.CompactSerialize()
	if _, err := verifier.(*OIDC).Verify(context.Background(), tok1); err == nil {
		t.Fatal("expected rejection of token without at+jwt typ header")
	}

	// Token with typ "at+jwt" should be accepted
	s2, _ := signerAtJwt.Sign(claims)
	tok2, _ := s2.CompactSerialize()
	ident, err := verifier.(*OIDC).Verify(context.Background(), tok2)
	if err != nil || ident.Subject != "alice" {
		t.Fatalf("expected valid at+jwt token, got error: %v, ident: %+v", err, ident)
	}
}

func TestOIDCClaimsAndPrefixSync(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "jwks_uri": server.URL + "/keys", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	verifier, err := New(context.Background(), config.Config{
		AuthMode:     "oidc",
		Issuer:       server.URL,
		Audience:     "papergo",
		GroupsClaim:  "realm_access.roles",
		GroupsPrefix: "papergo-",
	})
	if err != nil {
		t.Fatal(err)
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	claims, _ := json.Marshal(map[string]any{
		"iss":                server.URL,
		"aud":                "papergo",
		"sub":                "alice",
		"name":               "Alice Wonderland",
		"preferred_username": "alicew",
		"email":              "alice@example.com",
		"realm_access": map[string]any{
			"roles": []string{"papergo-admins", "papergo-editors", "unrelated-group"},
		},
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	})
	s, _ := signer.Sign(claims)
	tok, _ := s.CompactSerialize()

	ident, err := verifier.(*OIDC).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Subject != "alice" || ident.Name != "Alice Wonderland" || ident.PreferredUsername != "alicew" || ident.Email != "alice@example.com" {
		t.Fatalf("unexpected profile: %+v", ident)
	}
	if !ident.SyncGroups {
		t.Fatal("expected SyncGroups to be true")
	}
	expectedGroups := []string{"admins", "editors"}
	if len(ident.Groups) != len(expectedGroups) || ident.Groups[0] != "admins" || ident.Groups[1] != "editors" {
		t.Fatalf("unexpected groups: %+v", ident.Groups)
	}
}

func TestOIDCUserInfoFallback(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var userInfoCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                server.URL,
				"jwks_uri":                              server.URL + "/keys",
				"userinfo_endpoint":                     server.URL + "/userinfo",
				"authorization_endpoint":                server.URL + "/authorize",
				"token_endpoint":                        server.URL + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		case "/userinfo":
			userInfoCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":                "bob",
				"name":               "Bob Builder",
				"preferred_username": "bob",
				"email":              "bob@example.com",
				"groups":             []string{"developers"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	verifier, err := New(context.Background(), config.Config{
		AuthMode:           "oidc",
		Issuer:             server.URL,
		Audience:           "papergo",
		GroupsClaim:        "groups",
		GroupsFromUserInfo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	o := verifier.(*OIDC)
	o.userInfoCache["expired-token"] = userInfoCacheEntry{exp: time.Now().Add(-time.Minute)}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Token has sub only, no name or groups
	claims, _ := json.Marshal(map[string]any{
		"iss": server.URL,
		"aud": "papergo",
		"sub": "bob",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	})
	s, _ := signer.Sign(claims)
	tok, _ := s.CompactSerialize()

	ident, err := verifier.(*OIDC).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Subject != "bob" || ident.Name != "Bob Builder" || ident.Email != "bob@example.com" {
		t.Fatalf("unexpected userinfo identity: %+v", ident)
	}
	if len(ident.Groups) != 1 || ident.Groups[0] != "developers" {
		t.Fatalf("unexpected userinfo groups: %+v", ident.Groups)
	}
	if userInfoCalls.Load() != 1 {
		t.Fatalf("expected 1 userinfo call, got %d", userInfoCalls.Load())
	}
	if _, exists := o.userInfoCache["expired-token"]; exists {
		t.Fatal("expired UserInfo entry retained after caching a new token")
	}

	// Second verify call should be cached
	ident2, err := verifier.(*OIDC).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ident2.Subject != "bob" || len(ident2.Groups) != 1 || ident2.Groups[0] != "developers" {
		t.Fatalf("unexpected cached identity: %+v", ident2)
	}
	if userInfoCalls.Load() != 1 {
		t.Fatalf("expected userinfo call to be cached, got %d calls", userInfoCalls.Load())
	}
}

func TestUserInfoCacheCapacity(t *testing.T) {
	o := &OIDC{userInfoCache: make(map[string]userInfoCacheEntry)}
	expiry := time.Now().Add(time.Hour)
	o.cacheUserInfo("earliest-token", userInfoCacheEntry{exp: expiry.Add(-time.Minute)})
	for i := 1; i < maxUserInfoCacheEntries; i++ {
		o.cacheUserInfo(fmt.Sprintf("token-%d", i), userInfoCacheEntry{exp: expiry})
	}
	// Updating an existing token must not evict a different live token.
	o.cacheUserInfo("token-1", userInfoCacheEntry{exp: expiry})
	if len(o.userInfoCache) != maxUserInfoCacheEntries {
		t.Fatalf("updating a cached token changed the cache size: %d", len(o.userInfoCache))
	}
	o.cacheUserInfo("new-token", userInfoCacheEntry{exp: expiry})
	if len(o.userInfoCache) != maxUserInfoCacheEntries {
		t.Fatalf("UserInfo cache exceeded its capacity: %d", len(o.userInfoCache))
	}
	if _, exists := o.userInfoCache["earliest-token"]; exists {
		t.Fatal("earliest-expiring token was not evicted at capacity")
	}
	if _, exists := o.userInfoCache["new-token"]; !exists {
		t.Fatal("new token was not cached at capacity")
	}
}
