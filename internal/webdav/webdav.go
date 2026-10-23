// Package webdav serves WebDAV-enabled libraries to file clients such as
// Windows Explorer, macOS Finder and Microsoft Office. Every library is its own
// mount at /webdav/{libraryID}/: folders are collections and items are files
// whose bytes are the caller's visible revision. It implements RFC 4918 class 1
// and 2; locks live in this process, which matches the single-writer deployment.
//
// Authorization, visibility and every mutation go through the DMS service, so
// WebDAV clients see exactly what the REST API shows the same principal.
package webdav

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"net/url"
	"papergo/internal/auth"
	"papergo/internal/dal"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"strings"
)

const Prefix = "/webdav/"

const allowedMethods = "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, COPY, MOVE, PROPFIND, PROPPATCH, LOCK, UNLOCK"

type Handler struct {
	DMS       *dms.Service
	Auth      auth.Authenticator
	Storage   storage.Store
	Logger    *slog.Logger
	MaxUpload int64
	// PublicHost is the host clients use behind a reverse proxy; Destination
	// headers may name it as well as the request's Host.
	PublicHost string
	locks      *lockTable
}

func New(service *dms.Service, verifier auth.Authenticator, store storage.Store, logger *slog.Logger, maxUpload int64) *Handler {
	return &Handler{DMS: service, Auth: verifier, Storage: store, Logger: logger, MaxUpload: maxUpload, locks: newLockTable()}
}

// request is one authenticated WebDAV call on a library path.
type request struct {
	*http.Request
	subject string
	library string
	// path holds the decoded segments below the library; empty for the library itself.
	path []string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clients probe capabilities before authenticating.
	if r.Method == http.MethodOptions {
		w.Header().Set("DAV", "1, 2")
		w.Header().Set("MS-Author-Via", "DAV")
		w.Header().Set("Allow", allowedMethods)
		w.WriteHeader(http.StatusOK)
		return
	}
	r, subject, err := h.authenticate(r)
	if err != nil {
		if !errors.Is(err, dms.ErrForbidden) {
			h.Logger.ErrorContext(r.Context(), "webdav authentication failed", "error", err)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="PaperGo WebDAV", charset="UTF-8"`)
		h.status(w, http.StatusUnauthorized, "valid credentials are required")
		return
	}
	scope := "item.write"
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == "PROPFIND" {
		scope = "item.read"
	}
	if err = h.DMS.RequireScope(r.Context(), subject, scope); err != nil {
		h.fail(w, r, err)
		return
	}
	documentScope := "document.write"
	if scope == "item.read" {
		documentScope = "document.read"
	}
	if err = h.DMS.RequireScope(r.Context(), subject, documentScope); err != nil {
		h.fail(w, r, err)
		return
	}
	library, path, ok := parsePath(r.URL.EscapedPath())
	if !ok {
		h.status(w, http.StatusBadRequest, "invalid path")
		return
	}
	req := &request{Request: r, subject: subject, library: library, path: path}
	if library == "" {
		// The mount root only describes itself, so clients can walk up to it.
		if r.Method == "PROPFIND" {
			h.propfindRoot(w, req)
		} else {
			w.Header().Set("Allow", "OPTIONS, PROPFIND")
			h.status(w, http.StatusMethodNotAllowed, "map a library: "+Prefix+"{library id}/")
		}
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		err = h.get(w, req)
	case http.MethodPut:
		err = h.put(w, req)
	case http.MethodDelete:
		err = h.delete(w, req)
	case "MKCOL":
		err = h.mkcol(w, req)
	case "COPY", "MOVE":
		err = h.copyMove(w, req)
	case "PROPFIND":
		err = h.propfind(w, req)
	case "PROPPATCH":
		err = h.proppatch(w, req)
	case "LOCK":
		err = h.lock(w, req)
	case "UNLOCK":
		err = h.unlock(w, req)
	default:
		w.Header().Set("Allow", allowedMethods)
		h.status(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err != nil {
		h.fail(w, r, err)
	}
}

// authenticate accepts the deployment's trusted proxy assertion, or an API
// bearer token or Basic access token in bearer-authenticated deployments.
func (h *Handler) authenticate(r *http.Request) (*http.Request, string, error) {
	if h.Auth.Kind() == auth.TrustedProxy {
		ident, err := h.Auth.Authenticate(r)
		if err != nil {
			return r, "", dms.ErrForbidden
		}
		if err = h.DMS.Directory().EnsureUser(r.Context(), ident.Subject); err != nil {
			return r, "", err
		}
		if err = identity.RequireActive(r.Context(), dal.NewStore(h.DMS.Directory().DB), ident.Subject); err != nil {
			return r, "", dms.ErrForbidden
		}
		return r, ident.Subject, nil
	}
	scheme, secret, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	secret = strings.TrimSpace(secret)
	switch {
	case strings.EqualFold(scheme, "Bearer"):
		if strings.HasPrefix(secret, identity.AccessTokenPrefix) {
			token, err := h.DMS.Directory().AuthenticateAccessToken(r.Context(), secret)
			if err != nil {
				return r, "", dms.ErrForbidden
			}
			return r.WithContext(identity.WithAccessToken(r.Context(), token)), token.Subject, nil
		}
		ident, err := h.Auth.Authenticate(r)
		if err != nil {
			return r, "", dms.ErrForbidden
		}
		if err = h.DMS.Directory().SyncUser(r.Context(), identity.UserIdentity{
			Subject:           ident.Subject,
			Name:              ident.Name,
			PreferredUsername: ident.PreferredUsername,
			Email:             ident.Email,
			Groups:            ident.Groups,
			SyncGroups:        ident.SyncGroups,
		}); err != nil {
			return r, "", err
		}
		if err = identity.RequireActive(r.Context(), dal.NewStore(h.DMS.Directory().DB), ident.Subject); err != nil {
			return r, "", dms.ErrForbidden
		}
		return r, ident.Subject, nil
	case strings.EqualFold(scheme, "Basic"):
		_, password, ok := r.BasicAuth()
		if !ok {
			return r, "", dms.ErrForbidden
		}
		token, err := h.DMS.Directory().AuthenticateAccessToken(r.Context(), password)
		if err != nil {
			return r, "", dms.ErrForbidden
		}
		return r.WithContext(identity.WithAccessToken(r.Context(), token)), token.Subject, nil
	}
	return r, "", dms.ErrForbidden
}

// parsePath splits an escaped request path into a library ID and decoded
// segments. Escaped slashes stay inside their segment, where names reject them.
func parsePath(escaped string) (string, []string, bool) {
	rest, ok := strings.CutPrefix(escaped, strings.TrimSuffix(Prefix, "/"))
	if !ok || rest != "" && rest[0] != '/' {
		return "", nil, false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if parts[0] == "" {
		return "", nil, true
	}
	if _, err := uuid.Parse(parts[0]); err != nil || len(parts[0]) != 36 {
		return "", nil, false
	}
	path := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		name, err := url.PathUnescape(p)
		if err != nil || name == "." || name == ".." {
			return "", nil, false
		}
		if name != "" {
			path = append(path, name)
		}
	}
	return strings.ToLower(parts[0]), path, true
}

// href is the escaped URL path of a library entry; collections end in a slash.
func href(library string, path []string, collection bool) string {
	var b strings.Builder
	b.WriteString(Prefix + library)
	for _, name := range path {
		b.WriteString("/" + url.PathEscape(name))
	}
	if collection {
		b.WriteString("/")
	}
	return b.String()
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var validation *dms.ValidationError
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &validation):
		// Clients present 403 as "access denied" rather than a network failure.
		h.status(w, http.StatusForbidden, validation.Message)
	case errors.Is(err, dms.ErrContentUnavailable):
		h.status(w, http.StatusGone, "content unavailable")
	case errors.Is(err, dms.ErrNotFound):
		h.status(w, http.StatusNotFound, "not found")
	case errors.Is(err, dms.ErrForbidden):
		h.status(w, http.StatusForbidden, "access denied")
	case errors.Is(err, dms.ErrMissingParent):
		h.status(w, http.StatusConflict, "parent folder not found")
	case errors.Is(err, dms.ErrFolderNotEmpty):
		h.status(w, http.StatusConflict, "the folder is not empty")
	case errors.Is(err, dms.ErrConflict):
		h.status(w, http.StatusConflict, "conflicts with the current state of the library")
	case errors.Is(err, dms.ErrExists):
		h.status(w, http.StatusMethodNotAllowed, "a folder with this name already exists")
	case errors.Is(err, dms.ErrPreconditionFailed):
		h.status(w, http.StatusPreconditionFailed, "precondition failed")
	case errors.Is(err, errLocked):
		h.status(w, http.StatusLocked, "locked")
	case errors.Is(err, errBadRequest):
		h.status(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errBadGateway):
		h.status(w, http.StatusBadGateway, "destination must be in the same library")
	case errors.Is(err, errTooManyLocks):
		w.Header().Set("Retry-After", "60")
		h.status(w, http.StatusServiceUnavailable, "too many active locks")
	case errors.Is(err, storage.ErrUnsupportedFile):
		h.status(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, storage.ErrTooLarge) || errors.As(err, &tooLarge):
		h.status(w, http.StatusRequestEntityTooLarge, "file exceeds the upload limit")
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(r.Context().Err(), context.DeadlineExceeded):
		w.Header().Set("Retry-After", "1")
		h.status(w, http.StatusServiceUnavailable, "request exceeded the server time limit")
	case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
		// The client went away; nobody reads the response.
	default:
		h.Logger.ErrorContext(r.Context(), "webdav request failed", "method", r.Method, "error", err)
		h.status(w, http.StatusInternalServerError, "request could not be completed")
	}
}
func (h *Handler) status(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}

var (
	errBadRequest = errors.New("bad request")
	errBadGateway = errors.New("destination outside this library")
)

func badRequest(message string) error { return &protocolError{errBadRequest, message} }

// refused rejects an operation the protocol forbids, as 403 with its reason.
func refused(message string) error { return &dms.ValidationError{Message: message} }

type protocolError struct {
	kind    error
	message string
}

func (e *protocolError) Error() string { return e.message }
func (e *protocolError) Unwrap() error { return e.kind }
