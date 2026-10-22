package dms

import (
	"context"
	"errors"
	"strings"

	"papergo/internal/dal/access"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
)

// extensions are what features outside the core plug into the service.
// They are set while the executable starts, before the service is used.
type extensions struct {
	matcher  TextMatcher
	storage  []StorageReporter
	imported []ImportHook
}

// TextMatcher finds the items and folders whose text matches the search
// text of a listing or a collection query, such as a search index does.
// Without one, or where it answers ErrNoTextMatch, entries match by name.
type TextMatcher interface {
	MatchText(ctx context.Context, s *Service, subject string, in TextMatch) (TextMatches, error)
}

// TextMatch is the search text of a listing or a collection query, in one
// collection or, without one, across a workspace.
type TextMatch struct {
	Archive      refs.ArchiveSelection
	CollectionID string
	WorkspaceID  string
	Text         string
}

// TextMatches are the IDs of the items and folders matching on each
// surface. Capped reports that more matched than a query considers. The
// query authorizes, filters, sorts and pages them.
type TextMatches struct {
	Head, Published []string
	Capped          bool
}

// ErrNoTextMatch is a TextMatcher's answer where it does not search, such
// as a collection that turned it off: entries match by name instead.
var ErrNoTextMatch = errors.New("the text matcher does not search here")

// SetTextMatcher makes m match the search text of listings and collection
// queries.
func (s *Service) SetTextMatcher(m TextMatcher) { s.ext.matcher = m }

// matchedKey carries a text match made before a read transaction opened.
type matchedKey struct{}

type matched struct {
	archive                         refs.ArchiveSelection
	collectionID, workspaceID, text string
	match                           *content.SearchMatch
	capped                          bool
}

// matchFirst matches text in a collection (or a workspace) before a read
// transaction opens and carries the result to matchText in it: a matcher,
// such as a search index, can take far longer than the query, and must not
// hold a database connection meanwhile.
func (s *Service) matchFirst(ctx context.Context, subject, collectionID, workspaceID, text string, archive refs.ArchiveSelection) (context.Context, error) {
	if s.transaction || s.ext.matcher == nil || strings.TrimSpace(text) == "" {
		return ctx, nil
	}
	m, capped, err := s.matchText(ctx, subject, collectionID, workspaceID, text, archive)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, matchedKey{}, matched{archive: archive, collectionID: collectionID, workspaceID: workspaceID, text: strings.TrimSpace(text), match: m, capped: capped}), nil
}

// matchText decides how a listing or collection query matches text: by
// the matcher's IDs, with workspaces and collections still matching by
// name, or by name alone.
func (s *Service) matchText(ctx context.Context, subject, collectionID, workspaceID, text string, archive refs.ArchiveSelection) (*content.SearchMatch, bool, error) {
	text = strings.TrimSpace(text)
	if m, ok := ctx.Value(matchedKey{}).(matched); ok && m.archive == archive && m.collectionID == collectionID && m.workspaceID == workspaceID && m.text == text {
		return m.match, m.capped, nil
	}
	name := refs.FoldName(text)
	if s.ext.matcher != nil {
		found, err := s.ext.matcher.MatchText(ctx, s, subject, TextMatch{Archive: archive, CollectionID: collectionID, WorkspaceID: workspaceID, Text: text})
		if err == nil {
			return &content.SearchMatch{Name: name, Head: found.Head, Published: found.Published}, found.Capped, nil
		}
		if !errors.Is(err, ErrNoTextMatch) {
			return nil, false, err
		}
	}
	return &content.SearchMatch{Name: name, ByName: true}, false, nil
}

// StorageUsage is what an extension keeps outside the database, such as a
// search index, for the storage status.
type StorageUsage struct {
	Name    string `json:"name"`
	Store   string `json:"store"`
	Bytes   int64  `json:"bytes"`
	Entries int64  `json:"entries"`
}

// StorageReporter reports an extension's storage.
type StorageReporter func(context.Context) (StorageUsage, error)

// AddStorageReporter adds an extension's storage to the storage status.
func (s *Service) AddStorageReporter(r StorageReporter) { s.ext.storage = append(s.ext.storage, r) }

// ImportHook runs after an organization package import commits. Imported
// entries raise no change events, so an extension that keeps state derived
// from them, such as a search index, rebuilds it here.
type ImportHook func(context.Context, *Service) error

// OnImport adds a hook that runs after each import.
func (s *Service) OnImport(h ImportHook) { s.ext.imported = append(s.ext.imported, h) }

// Principal is who acts for subject: the subject, narrowed to the scopes of
// the access token the request carries.
func Principal(ctx context.Context, subject string) access.Principal { return principal(ctx, subject) }
