package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"net/http"
	"papergo/internal/config"
	"strings"
	"time"
)

type Verifier interface {
	Verify(context.Context, string) (string, error)
}
type Development struct {
	Token   string
	Subject string
}

func (d Development) Verify(_ context.Context, token string) (string, error) {
	if subtle.ConstantTimeCompare([]byte(d.Token), []byte(token)) != 1 || d.Token == "" {
		return "", errors.New("invalid token")
	}
	return d.Subject, nil
}

var ErrUnauthenticated = errors.New("valid bearer authentication is required")

// Authenticate verifies the request's "Authorization: Bearer" token and returns
// its subject. Every HTTP surface uses it, so all accept exactly the same tokens.
func Authenticate(r *http.Request, v Verifier) (string, error) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 8192 {
		return "", ErrUnauthenticated
	}
	subject, err := v.Verify(r.Context(), parts[1])
	if err != nil || subject == "" {
		return "", ErrUnauthenticated
	}
	return subject, nil
}

type OIDC struct{ verifier *oidc.IDTokenVerifier }

func (o OIDC) Verify(ctx context.Context, raw string) (string, error) {
	token, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return "", err
	}
	if token.Subject == "" || len(token.Subject) > 255 {
		return "", errors.New("invalid token subject")
	}
	return token.Subject, nil
}
func New(ctx context.Context, c config.Config) (Verifier, error) {
	if c.AuthMode == "development" {
		return Development{c.DevToken, c.DevSubject}, nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	return OIDC{provider.Verifier(&oidc.Config{ClientID: c.Audience})}, nil
}
