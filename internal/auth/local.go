package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	LocalMode  Kind = "local"
	sessionTTL      = 12 * time.Hour
)

// SessionCookie is the browser session set by POST /auth/login.
const SessionCookie = "papergo_session"

// Local accepts an HTTP Basic password or a session cookie for accounts that
// have a local password. It does not contact an identity provider.
type Local struct {
	DB *sql.DB
}

func (Local) Kind() Kind { return LocalMode }

func (l Local) Authenticate(r *http.Request) (Identity, error) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		if ident, err := l.Session(r.Context(), cookie.Value); err == nil {
			return ident, nil
		}
	}
	user, password, ok := r.BasicAuth()
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	ident, err := l.Check(r.Context(), user, password)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	return ident, nil
}

// Check verifies a username and password against the stored administrator.
func (l Local) Check(ctx context.Context, username, password string) (Identity, error) {
	username = strings.TrimSpace(username)
	var hash, name string
	var active bool
	var deleted sql.NullTime
	err := l.DB.QueryRowContext(ctx, "SELECT password_hash, display_name, active, deleted_at FROM identity_users WHERE id=? AND service=0", username).Scan(&hash, &name, &active, &deleted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Identity{}, err
	}
	if err != nil || !active || deleted.Valid || !PasswordMatches(hash, password) {
		return Identity{}, ErrUnauthenticated
	}
	if name == "" {
		name = username
	}
	return Identity{Subject: username, Name: name, PreferredUsername: username}, nil
}

// Login checks the password and returns a new session cookie value.
func (l Local) Login(ctx context.Context, username, password string) (string, Identity, error) {
	ident, err := l.Check(ctx, username, password)
	if err != nil {
		return "", Identity{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return "", Identity{}, err
	}
	secret, err := randomToken(32)
	if err != nil {
		return "", Identity{}, err
	}
	if _, err = l.DB.ExecContext(ctx, "INSERT INTO identity_sessions(id,created_at,subject,secret_hash,expires_at) VALUES(?,CURRENT_TIMESTAMP,?,?,?)", id, ident.Subject, sha256Hex(secret), time.Now().UTC().Add(sessionTTL)); err != nil {
		return "", Identity{}, err
	}
	return id + "." + secret, ident, nil
}

// Session resolves a cookie value to the signed-in account.
func (l Local) Session(ctx context.Context, cookie string) (Identity, error) {
	id, secret, ok := strings.Cut(cookie, ".")
	if !ok || id == "" || secret == "" {
		return Identity{}, ErrUnauthenticated
	}
	var subject, hash, name string
	var expires time.Time
	err := l.DB.QueryRowContext(ctx, "SELECT s.subject, s.secret_hash, s.expires_at, u.display_name FROM identity_sessions s JOIN identity_users u ON u.id=s.subject WHERE s.id=? AND u.active=1 AND u.deleted_at IS NULL AND u.service=0", id).Scan(&subject, &hash, &expires, &name)
	if err != nil || time.Now().After(expires) || subtle.ConstantTimeCompare([]byte(hash), []byte(sha256Hex(secret))) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	if name == "" {
		name = subject
	}
	return Identity{Subject: subject, Name: name, PreferredUsername: subject}, nil
}

// Logout removes the session carried by the cookie value.
func (l Local) Logout(ctx context.Context, cookie string) error {
	id, _, ok := strings.Cut(cookie, ".")
	if !ok || id == "" {
		return nil
	}
	_, err := l.DB.ExecContext(ctx, "DELETE FROM identity_sessions WHERE id=?", id)
	return err
}
