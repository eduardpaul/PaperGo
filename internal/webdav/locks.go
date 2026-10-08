package webdav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"papergo/internal/dms"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Locks are write locks (RFC 4918 §6) held in memory by the single API
// process; a restart releases them. Clients refresh before the granted timeout.

var (
	errLocked       = errors.New("resource is locked")
	errNoLock       = errors.New("no matching lock")
	errTooManyLocks = errors.New("too many active locks")
)

const (
	maxLockTimeout = time.Hour
	maxLocks       = 10000
)

type lock struct {
	token     string
	key       string // library ID plus case-folded path, like dms name keys
	href      string
	exclusive bool
	infinite  bool
	owner     string // the principal holding the lock
	ownerXML  string // rendered by this server from the client's owner href or text
	timeout   time.Duration
	expires   time.Time
}

// covers reports whether the lock applies to the resource at key.
func (l *lock) covers(key string) bool { return l.key == key || l.infinite && below(key, l.key) }

func below(key, root string) bool { return strings.HasPrefix(key, root+"/") }

func lockKey(library string, path []string) string {
	var b strings.Builder
	b.WriteString(library)
	for _, name := range path {
		b.WriteString("/" + strings.ToLower(name))
	}
	return b.String()
}

type lockTable struct {
	mu    sync.Mutex
	locks map[string]*lock
}

func newLockTable() *lockTable { return &lockTable{locks: map[string]*lock{}} }

// expire drops elapsed locks; callers hold mu.
func (t *lockTable) expire(now time.Time) {
	for token, l := range t.locks {
		if now.After(l.expires) {
			delete(t.locks, token)
		}
	}
}
func (t *lockTable) create(l *lock) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.expire(now)
	if len(t.locks) >= maxLocks {
		return errTooManyLocks
	}
	for _, o := range t.locks {
		if (o.covers(l.key) || l.infinite && below(o.key, l.key)) && (l.exclusive || o.exclusive) {
			return errLocked
		}
	}
	l.token = "urn:uuid:" + uuid.NewString()
	l.expires = now.Add(l.timeout)
	t.locks[l.token] = l
	return nil
}
func (t *lockTable) refresh(subject string, tokens []string, key string, timeout time.Duration) (lock, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.expire(now)
	for _, token := range tokens {
		if l := t.locks[token]; l != nil && l.owner == subject && l.covers(key) {
			l.timeout, l.expires = timeout, now.Add(timeout)
			return *l, nil
		}
	}
	return lock{}, errNoLock
}

// confirm allows changing key (and, for tree, everything below it) only when
// the caller submits, and owns, every lock that applies. A lock on the direct
// parent also guards adding or removing key. Tokens of locks that no longer
// exist are ignored, so an expired lock never blocks its former holder.
func (t *lockTable) confirm(subject string, tokens []string, key string, tree bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire(time.Now())
	parent := ""
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		parent = key[:i]
	}
	for _, l := range t.locks {
		if !l.covers(key) && !(tree && below(l.key, key)) && l.key != parent {
			continue
		}
		if l.owner != subject || !slices.Contains(tokens, l.token) {
			return errLocked
		}
	}
	return nil
}
func (t *lockTable) unlock(subject, token, key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire(time.Now())
	l := t.locks[token]
	if l == nil || !l.covers(key) {
		return errNoLock
	}
	if l.owner != subject {
		return dms.ErrForbidden
	}
	delete(t.locks, token)
	return nil
}

// release drops locks on key and below it, once that resource is gone.
func (t *lockTable) release(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for token, l := range t.locks {
		if l.key == key || below(l.key, key) {
			delete(t.locks, token)
		}
	}
}

// snapshot copies the live locks, for consistent lock discovery in one response.
func (t *lockTable) snapshot() []lock {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire(time.Now())
	out := make([]lock, 0, len(t.locks))
	for _, l := range t.locks {
		out = append(out, *l)
	}
	return out
}
func (t *lockTable) remove(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.locks, token)
}

// ifTokens extracts the coded URLs of an If header (RFC 4918 §10.4). Clients
// use it to submit lock tokens; resource tags among them never match a token.
var codedURL = regexp.MustCompile(`<([^<>]+)>`)

func ifTokens(header string) []string {
	var out []string
	for _, m := range codedURL.FindAllStringSubmatch(header, 32) {
		out = append(out, m[1])
	}
	return out
}

func (h *Handler) confirm(r *request, path []string, tree bool) error {
	return h.locks.confirm(r.subject, ifTokens(r.Header.Get("If")), lockKey(r.library, path), tree)
}

type lockInfo struct {
	XMLName   xml.Name `xml:"DAV: lockinfo"`
	LockScope struct {
		Exclusive *struct{} `xml:"DAV: exclusive"`
		Shared    *struct{} `xml:"DAV: shared"`
	} `xml:"DAV: lockscope"`
	LockType struct {
		Write *struct{} `xml:"DAV: write"`
	} `xml:"DAV: locktype"`
	// Only an owner href or text is kept: echoing the client's raw XML would
	// carry namespace prefixes that are undeclared in our responses.
	Owner *struct {
		Href string `xml:"DAV: href"`
		Text string `xml:",chardata"`
	} `xml:"DAV: owner"`
}

func (h *Handler) lock(w http.ResponseWriter, r *request) error {
	timeout := lockTimeout(r.Header.Get("Timeout"))
	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		l, err := h.locks.refresh(r.subject, ifTokens(r.Header.Get("If")), lockKey(r.library, r.path), timeout)
		if err != nil {
			return dms.ErrPreconditionFailed
		}
		return writeLock(w, http.StatusOK, l)
	}
	var info lockInfo
	if err = xml.Unmarshal(body, &info); err != nil {
		return badRequest("invalid lockinfo")
	}
	if info.LockType.Write == nil || (info.LockScope.Exclusive == nil) == (info.LockScope.Shared == nil) {
		return badRequest("only exclusive or shared write locks are supported")
	}
	infinite := true
	switch r.Header.Get("Depth") {
	case "", "infinity":
	case "0":
		infinite = false
	default:
		return badRequest("lock depth must be 0 or infinity")
	}
	existing, err := h.DMS.WriteTarget(r.Context(), r.subject, r.library, r.path, dms.FileConditions{})
	if err != nil {
		return err
	}
	collection := existing != nil && existing.Collection()
	l := &lock{key: lockKey(r.library, r.path), href: href(r.library, r.path, collection), exclusive: info.LockScope.Exclusive != nil, infinite: infinite && collection, owner: r.subject, timeout: timeout}
	if info.Owner != nil {
		if href := strings.TrimSpace(info.Owner.Href); href != "" {
			l.ownerXML = "<D:href>" + escape(href) + "</D:href>"
		} else {
			l.ownerXML = escape(strings.TrimSpace(info.Owner.Text))
		}
	}
	// Creating the file adds it to its folder, which a parent lock guards.
	if existing == nil {
		if err = h.confirm(r, r.path, false); err != nil {
			return err
		}
	}
	if err = h.locks.create(l); err != nil {
		return err
	}
	status := http.StatusOK
	if existing == nil {
		// Locking an unmapped URL creates an empty file (RFC 4918 §7.3).
		if _, _, err = h.store(r, r.path, bytes.NewReader(nil), contentType(r.path[len(r.path)-1], ""), dms.FileConditions{IfNoneMatch: "*"}, nil, nil); err != nil {
			h.locks.remove(l.token)
			return err
		}
		status = http.StatusCreated
	}
	w.Header().Set("Lock-Token", "<"+l.token+">")
	return writeLock(w, status, *l)
}
func (h *Handler) unlock(w http.ResponseWriter, r *request) error {
	token := r.Header.Get("Lock-Token")
	if len(token) < 3 || token[0] != '<' || token[len(token)-1] != '>' {
		return badRequest("Lock-Token must be a coded URL")
	}
	err := h.locks.unlock(r.subject, token[1:len(token)-1], lockKey(r.library, r.path))
	if errors.Is(err, errNoLock) {
		h.status(w, http.StatusConflict, "no matching lock on this resource")
		return nil
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// lockTimeout grants the first acceptable requested timeout, capped at maxLockTimeout.
func lockTimeout(header string) time.Duration {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if strings.EqualFold(v, "Infinite") {
			return maxLockTimeout
		}
		if s, ok := strings.CutPrefix(v, "Second-"); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
				return time.Duration(min(n, int64(maxLockTimeout/time.Second))) * time.Second
			}
		}
	}
	return maxLockTimeout
}
func writeLock(w http.ResponseWriter, status int, l lock) error {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, err := io.WriteString(w, xml.Header+`<D:prop xmlns:D="DAV:"><D:lockdiscovery>`+activeLock(l)+`</D:lockdiscovery></D:prop>`)
	return err
}
func activeLock(l lock) string {
	scope, depth := "shared", "0"
	if l.exclusive {
		scope = "exclusive"
	}
	if l.infinite {
		depth = "infinity"
	}
	owner := ""
	if l.ownerXML != "" {
		owner = "<D:owner>" + l.ownerXML + "</D:owner>"
	}
	return `<D:activelock><D:locktype><D:write/></D:locktype><D:lockscope><D:` + scope + `/></D:lockscope><D:depth>` + depth + `</D:depth>` + owner +
		`<D:timeout>Second-` + strconv.Itoa(int(l.timeout/time.Second)) + `</D:timeout><D:locktoken><D:href>` + escape(l.token) + `</D:href></D:locktoken>` +
		`<D:lockroot><D:href>` + escape(l.href) + `</D:href></D:lockroot></D:activelock>`
}
