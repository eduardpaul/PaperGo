package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
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
	DAL *dal.Store
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

// account returns the local account of a subject when it may sign in: an
// active, undeleted user who is not a service identity.
func (l Local) account(ctx context.Context, subject string) (directory.User, bool, error) {
	u, err := l.DAL.Directory().User(ctx, subject)
	if dberr.IsNotFound(err) {
		return directory.User{}, false, nil
	}
	if err != nil {
		return directory.User{}, false, err
	}
	return u, u.Live() && !u.Service, nil
}

func identityOf(u directory.User) Identity {
	name := u.DisplayName
	if name == "" {
		name = u.ID
	}
	return Identity{Subject: u.ID, Name: name, PreferredUsername: u.ID}
}

// Check verifies a username and password against the stored administrator.
func (l Local) Check(ctx context.Context, username, password string) (Identity, error) {
	u, ok, err := l.account(ctx, strings.TrimSpace(username))
	if err != nil {
		return Identity{}, err
	}
	if !ok || !PasswordMatches(u.PasswordHash, password) {
		return Identity{}, ErrUnauthenticated
	}
	return identityOf(u), nil
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
	session := directory.Session{ID: id, Subject: ident.Subject, SecretHash: sha256Hex(secret), ExpiresAt: time.Now().Add(sessionTTL)}
	if err = l.DAL.Directory().StartSession(ctx, session); err != nil {
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
	session, err := l.DAL.Directory().Session(ctx, id)
	if err != nil || time.Now().After(session.ExpiresAt) || subtle.ConstantTimeCompare([]byte(session.SecretHash), []byte(sha256Hex(secret))) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	u, ok, err := l.account(ctx, session.Subject)
	if err != nil || !ok {
		return Identity{}, ErrUnauthenticated
	}
	return identityOf(u), nil
}

// Logout removes the session carried by the cookie value.
func (l Local) Logout(ctx context.Context, cookie string) error {
	id, _, ok := strings.Cut(cookie, ".")
	if !ok || id == "" {
		return nil
	}
	return l.DAL.Directory().EndSession(ctx, id)
}
