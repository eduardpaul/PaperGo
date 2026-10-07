package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"github.com/go-jose/go-jose/v4"
	"net/http"
	"net/http/httptest"
	"papergo/internal/config"
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
			subject, e := verifier.Verify(context.Background(), raw)
			if tc.valid {
				if e != nil || subject != "alice" {
					t.Fatalf("valid token rejected: %v", e)
				}
				bytes := []byte(raw)
				if bytes[len(bytes)-5] == 'A' {
					bytes[len(bytes)-5] = 'B'
				} else {
					bytes[len(bytes)-5] = 'A'
				}
				if _, e = verifier.Verify(context.Background(), string(bytes)); e == nil {
					t.Fatal("tampered signature accepted")
				}
			} else if e == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}
