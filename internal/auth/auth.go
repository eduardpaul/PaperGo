package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"net/http"
	"papergo/internal/config"
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
