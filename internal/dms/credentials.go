package dms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"papergo/ent"
	"papergo/ent/webdavcredential"
	"strings"
	"time"
	"unicode/utf8"
)

// WebDAV clients such as Windows Explorer authenticate with HTTP Basic, so a
// principal issues itself app passwords. The password is shown once; only its
// SHA-256 is stored. 256 random bits make a hash lookup safe without stretching.

const (
	credentialPrefix = "pgdav_"
	maxCredentials   = 20
)

type WebDAVCredentialInput struct {
	Label     string     `json:"label" minLength:"1" maxLength:"100" doc:"Trimmed name identifying the device or client."`
	ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"Optional future expiry; credentials without one last until revoked."`
}

// NewWebDAVCredential carries the password, which is never readable again.
type NewWebDAVCredential struct {
	*ent.WebDAVCredential
	Password string `json:"password"`
}

func (s *Service) CreateWebDAVCredential(ctx context.Context, subject string, in WebDAVCredentialInput) (out *NewWebDAVCredential, err error) {
	if strings.TrimSpace(in.Label) != in.Label || in.Label == "" || utf8.RuneCountInString(in.Label) > 100 {
		return nil, invalid("label must be a trimmed string of 1 to 100 characters")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return nil, invalid("expires_at must be in the future")
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, err
	}
	password := credentialPrefix + base64.RawURLEncoding.EncodeToString(secret)
	err = s.write(ctx, func(t *Service) error {
		n, e := t.Client.WebDAVCredential.Query().Where(webdavcredential.SubjectEQ(subject)).Count(ctx)
		if e != nil {
			return e
		}
		if n >= maxCredentials {
			return invalid("at most 20 WebDAV credentials per principal; revoke one first")
		}
		b := t.Client.WebDAVCredential.Create().SetSubject(subject).SetLabel(in.Label).SetSecretHash(credentialHash(password))
		if in.ExpiresAt != nil {
			b.SetExpiresAt(in.ExpiresAt.UTC())
		}
		c, e := b.Save(ctx)
		out = &NewWebDAVCredential{WebDAVCredential: c, Password: password}
		return e
	})
	return
}

// WebDAVCredentials lists the caller's own credentials, oldest first.
func (s *Service) WebDAVCredentials(ctx context.Context, subject string) ([]*ent.WebDAVCredential, error) {
	return s.Client.WebDAVCredential.Query().Where(webdavcredential.SubjectEQ(subject)).Order(ent.Asc(webdavcredential.FieldCreatedAt), ent.Asc(webdavcredential.FieldID)).Limit(maxCredentials).All(ctx)
}

// RevokeWebDAVCredential deletes one of the caller's credentials.
func (s *Service) RevokeWebDAVCredential(ctx context.Context, subject, id string) error {
	return s.write(ctx, func(t *Service) error {
		n, e := t.Client.WebDAVCredential.Delete().Where(webdavcredential.IDEQ(id), webdavcredential.SubjectEQ(subject)).Exec(ctx)
		if e == nil && n == 0 {
			e = ErrNotFound
		}
		return e
	})
}

// AuthenticateWebDAV returns the principal of an unexpired app password.
func (s *Service) AuthenticateWebDAV(ctx context.Context, password string) (string, error) {
	if !strings.HasPrefix(password, credentialPrefix) || len(password) > 128 {
		return "", ErrForbidden
	}
	c, err := s.Client.WebDAVCredential.Query().Where(webdavcredential.SecretHashEQ(credentialHash(password))).Only(ctx)
	if ent.IsNotFound(err) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if c.ExpiresAt != nil && !c.ExpiresAt.After(time.Now()) {
		return "", ErrForbidden
	}
	return c.Subject, nil
}
func credentialHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}
