// Package comments is PaperGo's discussion building block: people talk
// about an item where it lives, in threads of comments, and bring others in
// by mentioning them. Mentions and replies reach people as notifications.
//
// Who takes part follows the item: a collection's mode decides whether its
// discussions are off, open to the people who can read drafts
// (collaborators), or to everyone who can read the item (readers). The mode
// is inherited from the workspace and the organization, and the deployment
// can turn comments off altogether. A mention never shares the item.
package comments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	commentstore "papergo/internal/dal/comments"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	grantstore "papergo/internal/dal/grants"
	"papergo/internal/dal/notifications"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/settings"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/notify"
	"papergo/internal/recipients"
)

// Mode is who takes part in a collection's discussions.
type Mode string

const (
	// ModeOff hides discussions; their comments are kept.
	ModeOff Mode = "off"
	// ModeCollaborators opens discussions to people who can read drafts.
	ModeCollaborators Mode = "collaborators"
	// ModeReaders opens discussions to everyone who can read the item.
	ModeReaders Mode = "readers"
)

func (m Mode) Valid() bool { return m == ModeOff || m == ModeCollaborators || m == ModeReaders }

// Origin is the level an effective mode comes from.
type Origin string

const (
	OriginDeployment   Origin = "deployment"
	OriginOrganization Origin = "organization"
	OriginWorkspace    Origin = "workspace"
	OriginCollection   Origin = "collection"
	OriginDefault      Origin = "default"
)

// Effective is the mode that applies somewhere and where it comes from.
type Effective struct {
	Mode Mode   `json:"mode"`
	From Origin `json:"from"`
}

// Limits of a discussion.
const (
	MaxMentions = 50
	// MaxCounted bounds the items one count request names.
	MaxCounted = 200
)

// Scope is the module scope that writes to discussions.
const Scope = "comment.write"

// mentionPattern finds mentions in a comment: <@user:subject>.
var mentionPattern = regexp.MustCompile(`<@user:([^<>\s]{1,255})>`)

// Mentions returns the people a comment's text mentions, in order and
// without duplicates.
func Mentions(body string) []string {
	var out []string
	for _, m := range mentionPattern.FindAllStringSubmatch(body, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// Service reads and writes discussions for the API, each call in its own
// transaction. Enabled is the deployment's switch.
type Service struct {
	DMS     *dms.Service
	Enabled bool
}

// organizationValue is the organization's setting: a hard switch and the
// default mode.
type organizationValue struct {
	Enabled *bool `json:"enabled,omitempty"`
	Mode    Mode  `json:"mode,omitempty"`
}

// placeValue is a workspace's or collection's setting; no mode inherits.
type placeValue struct {
	Mode Mode `json:"mode,omitempty"`
}

// effective resolves the mode of a workspace, collection or anything in
// one.
func (s *Service) effective(ctx context.Context, t *dms.Service, r *resources.Resource) (Effective, error) {
	if !s.Enabled {
		return Effective{ModeOff, OriginDeployment}, nil
	}
	collection := ""
	switch r.Kind {
	case refs.KindList, refs.KindLibrary:
		collection = r.ID
	case refs.KindItem, refs.KindFolder:
		if r.ContainerID != nil {
			collection = *r.ContainerID
		}
	}
	ids := []string{r.WorkspaceID}
	if collection != "" {
		ids = append(ids, collection)
	}
	chain, err := t.DAL.Settings().Chain(ctx, settings.KeyComments, ids)
	if err != nil {
		return Effective{}, err
	}
	var org organizationValue
	if v, ok := chain[""]; ok {
		if err = json.Unmarshal(v.Value, &org); err != nil {
			return Effective{}, err
		}
	}
	if org.Enabled != nil && !*org.Enabled {
		return Effective{ModeOff, OriginOrganization}, nil
	}
	for _, level := range []struct {
		id     string
		origin Origin
	}{{collection, OriginCollection}, {r.WorkspaceID, OriginWorkspace}} {
		if v, ok := chain[level.id]; ok && level.id != "" {
			var place placeValue
			if err = json.Unmarshal(v.Value, &place); err != nil {
				return Effective{}, err
			}
			if place.Mode != "" {
				return Effective{place.Mode, level.origin}, nil
			}
		}
	}
	if org.Mode != "" {
		return Effective{org.Mode, OriginOrganization}, nil
	}
	return Effective{ModeCollaborators, OriginDefault}, nil
}

// access is what a person may do in an item's discussion.
type access struct {
	item                *resources.Resource
	mode                Effective
	read, post          bool
	write, manage       bool
	scope, archivedItem bool
}

// discussion reads the item and what subject may do in its discussion.
// A person who cannot read the item gets its error.
func (s *Service) discussion(ctx context.Context, t *dms.Service, subject, itemID string) (access, error) {
	item, err := t.Authorize(ctx, subject, itemID, "read")
	if err != nil {
		return access{}, err
	}
	if item.Kind != refs.KindItem {
		return access{}, dms.Invalid("comments are on items")
	}
	a := access{item: item, archivedItem: item.Archived}
	if a.mode, err = s.effective(ctx, t, item); err != nil {
		return a, err
	}
	switch a.mode.Mode {
	case ModeCollaborators:
		a.read, err = notify.Can(ctx, t, subject, itemID, "read_draft")
	case ModeReaders:
		a.read, err = notify.CanSee(ctx, t, subject, itemID)
	}
	if err != nil || !a.read {
		return a, err
	}
	if a.write, err = notify.Can(ctx, t, subject, itemID, "write"); err != nil {
		return a, err
	}
	if a.manage, err = notify.Can(ctx, t, subject, itemID, "manage"); err != nil {
		return a, err
	}
	switch err = identity.RequireScope(ctx, t.DAL, subject, Scope); {
	case err == nil:
		a.scope = true
	case !errors.Is(err, identity.ErrForbidden):
		return a, err
	}
	a.post = a.scope && !a.archivedItem
	return a, nil
}

// refuse refuses writes where discussions are off or hidden, without the
// comment.write scope, and on archived items.
func (a access) refuse() error {
	if a.mode.Mode == ModeOff {
		return fmt.Errorf("%w: comments are off here", dms.ErrConflict)
	}
	if !a.read {
		return dms.ErrForbidden
	}
	if !a.scope {
		return dms.ErrForbidden
	}
	if a.archivedItem {
		return fmt.Errorf("%w: the item is archived; its discussion is read only", dms.ErrConflict)
	}
	return nil
}

// Person is someone a comment names, with the name to show.
type Person struct {
	Subject string `json:"subject"`
	Name    string `json:"name"`
}

// View is a comment as the API shows it.
type View struct {
	ID         string     `json:"id"`
	ItemID     string     `json:"item_id"`
	ThreadID   string     `json:"thread_id"`
	Author     Person     `json:"author"`
	Source     string     `json:"source"`
	WorkflowID *string    `json:"workflow_id,omitempty"`
	Body       string     `json:"body"`
	Mentions   []Person   `json:"mentions"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`
	Deleted    bool       `json:"deleted,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy *Person    `json:"resolved_by,omitempty"`
	Version    int64      `json:"version"`
	CanEdit    bool       `json:"can_edit"`
	CanDelete  bool       `json:"can_delete"`
	// CanResolve, on a thread's first comment, tells whether the reader may
	// resolve or reopen the thread.
	CanResolve bool `json:"can_resolve,omitempty"`
}

// Thread is a thread's first comment and its replies, oldest first.
type Thread struct {
	View
	Replies []View `json:"replies"`
}

// names returns display names of subjects, their subject when unknown.
func names(ctx context.Context, t *dms.Service, subjects []string) (map[string]string, error) {
	out := map[string]string{}
	if len(subjects) == 0 {
		return out, nil
	}
	rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: subjects})
	if err != nil {
		return nil, err
	}
	for _, s := range subjects {
		out[s] = s
	}
	for _, u := range rows {
		if u.DisplayName != "" {
			out[u.ID] = u.DisplayName
		}
	}
	return out, nil
}

// viewer turns comments into views for subject, naming people once.
type viewer struct {
	subject string
	a       access
	roots   map[string]commentstore.Comment
	names   map[string]string
}

func newViewer(ctx context.Context, t *dms.Service, subject string, a access, rows []commentstore.Comment, roots map[string]commentstore.Comment) (viewer, error) {
	var people []string
	for _, c := range rows {
		people = append(people, c.Author, c.ResolvedBy)
		people = append(people, c.Mentions...)
	}
	people = slices.DeleteFunc(slices.Compact(slices.Sorted(slices.Values(people))), func(s string) bool { return s == "" })
	n, err := names(ctx, t, people)
	return viewer{subject: subject, a: a, roots: roots, names: n}, err
}

func (v viewer) person(subject string) Person {
	return Person{Subject: subject, Name: v.names[subject]}
}

func (v viewer) view(c commentstore.Comment) View {
	out := View{ID: c.ID, ItemID: c.ItemID, ThreadID: c.ThreadID, Author: v.person(c.Author), Source: string(c.Source), Body: c.Body,
		Mentions: []Person{}, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, EditedAt: c.EditedAt, Deleted: c.DeletedAt != nil,
		ResolvedAt: c.ResolvedAt, Version: c.Version}
	if c.WorkflowID != "" {
		out.WorkflowID = &c.WorkflowID
	}
	for _, m := range c.Mentions {
		out.Mentions = append(out.Mentions, v.person(m))
	}
	if c.ResolvedBy != "" {
		p := v.person(c.ResolvedBy)
		out.ResolvedBy = &p
	}
	if c.DeletedAt == nil && v.a.post {
		out.CanEdit = c.Author == v.subject
		out.CanDelete = c.Author == v.subject || v.a.manage
		if c.ID == c.ThreadID {
			out.CanResolve = c.Author == v.subject || v.a.write || v.a.manage
		}
	}
	return out
}

// Page is a page of an item's threads with what the reader may do there.
type Page struct {
	Data       []Thread
	NextCursor string
	Mode       Effective
	CanRead    bool
	CanPost    bool
}

// APIPage shows the page with its discussion state.
func (p Page) APIPage() (map[string]any, string) {
	return map[string]any{"value": p.Data, "mode": p.Mode.Mode, "mode_from": p.Mode.From, "can_read": p.CanRead, "can_post": p.CanPost}, p.NextCursor
}

// Status selects threads by state.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
	StatusAll      Status = "all"
)

// Threads lists an item's threads, oldest first, each with its replies.
// Where discussions are off or hidden from the reader, it lists none and
// says why.
func (s *Service) Threads(ctx context.Context, subject, itemID string, status Status, limit int, after string) (Page, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Page, error) {
		a, err := s.discussion(ctx, t, subject, itemID)
		if err != nil {
			return Page{}, err
		}
		out := Page{Data: []Thread{}, Mode: a.mode, CanRead: a.read, CanPost: a.read && a.post && a.mode.Mode != ModeOff}
		if !a.read {
			return out, nil
		}
		f := commentstore.Threads{ItemID: itemID}
		switch status {
		case StatusOpen, "":
			open := false
			f.Resolved = &open
		case StatusResolved:
			resolved := true
			f.Resolved = &resolved
		case StatusAll:
		default:
			return out, dms.Invalid("status is open, resolved or all")
		}
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		f.Limit = limit + 1
		if after != "" {
			key, err := parseCursor(after)
			if err != nil {
				return out, err
			}
			f.After = &key
		}
		roots, err := t.DAL.Comments().Roots(ctx, f)
		if err != nil {
			return out, err
		}
		if len(roots) > limit {
			roots = roots[:limit]
			last := roots[len(roots)-1]
			out.NextCursor = cursor(last.CreatedAt, last.ID)
		}
		ids := make([]string, len(roots))
		byID := map[string]commentstore.Comment{}
		for i, r := range roots {
			ids[i], byID[r.ID] = r.ID, r
		}
		replies, err := t.DAL.Comments().Replies(ctx, ids)
		if err != nil {
			return out, err
		}
		v, err := newViewer(ctx, t, subject, a, append(slices.Clone(roots), replies...), byID)
		if err != nil {
			return out, err
		}
		threads := map[string]*Thread{}
		for _, r := range roots {
			out.Data = append(out.Data, Thread{View: v.view(r), Replies: []View{}})
		}
		for i := range out.Data {
			threads[out.Data[i].ID] = &out.Data[i]
		}
		for _, r := range replies {
			if r.DeletedAt != nil {
				continue
			}
			th := threads[r.ThreadID]
			th.Replies = append(th.Replies, v.view(r))
		}
		return out, nil
	})
}

func cursor(at time.Time, id string) string {
	return at.UTC().Format(time.RFC3339Nano) + "|" + id
}

func parseCursor(c string) (commentstore.Key, error) {
	at, id, ok := strings.Cut(c, "|")
	created, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || id == "" {
		return commentstore.Key{}, dms.Invalid("$skiptoken is not a cursor of this list")
	}
	return commentstore.Key{CreatedAt: created, ID: id}, nil
}

// Get reads one comment.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (View, error) {
		c, a, err := s.load(ctx, t, subject, id)
		if err != nil {
			return View{}, err
		}
		v, err := newViewer(ctx, t, subject, a, []commentstore.Comment{c}, nil)
		return v.view(c), err
	})
}

// load reads a comment the subject may read.
func (s *Service) load(ctx context.Context, t *dms.Service, subject, id string) (commentstore.Comment, access, error) {
	c, err := t.DAL.Comments().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return c, access{}, dms.ErrNotFound
	}
	if err != nil {
		return c, access{}, err
	}
	a, err := s.discussion(ctx, t, subject, c.ItemID)
	if errors.Is(err, dms.ErrForbidden) || (err == nil && !a.read) {
		return c, a, dms.ErrNotFound
	}
	return c, a, err
}

// Input is a comment as people and workflows write it.
type Input struct {
	Body string
	// ThreadID answers a thread; empty starts one.
	ThreadID string
}

// mentionsOf reads a comment's mentions and checks that those it adds to
// kept, the comment's mentions before an edit, name active people. People
// who left since they were mentioned stay mentioned.
func mentionsOf(ctx context.Context, t *dms.Service, body string, kept []string) ([]string, error) {
	people := Mentions(body)
	if len(people) > MaxMentions {
		return nil, dms.Invalid(fmt.Sprintf("a comment mentions at most %d people", MaxMentions))
	}
	added := slices.DeleteFunc(slices.Clone(people), func(p string) bool { return slices.Contains(kept, p) })
	if len(added) == 0 {
		return people, nil
	}
	rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: added})
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, u := range rows {
		active[u.ID] = u.Active && u.DeletedAt == nil
	}
	var unknown []string
	for _, p := range added {
		if !active[p] {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) > 0 {
		return nil, dms.Invalid("unknown or inactive people mentioned: " + strings.Join(unknown, ", "))
	}
	return people, nil
}

func checkBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > commentstore.MaxBody {
		return "", dms.Invalid(fmt.Sprintf("a comment has 1 to %d characters", commentstore.MaxBody))
	}
	return body, nil
}

// Create writes subject's comment, or the workflow run's that makes the
// request, and tells the people it mentions and the thread's participants.
func (s *Service) Create(ctx context.Context, subject, run, itemID string, in Input) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		a, err := s.discussion(ctx, t, subject, itemID)
		if err != nil {
			return err
		}
		if err = a.refuse(); err != nil {
			return err
		}
		body, err := checkBody(in.Body)
		if err != nil {
			return err
		}
		mentions, err := mentionsOf(ctx, t, body, nil)
		if err != nil {
			return err
		}
		c := commentstore.Comment{ItemID: itemID, ThreadID: in.ThreadID, Author: subject, Source: commentstore.SourceUser, Body: body, Mentions: mentions}
		if run != "" {
			r, err := t.DAL.Runs().Find(ctx, run)
			if err != nil {
				return err
			}
			if r.WorkspaceID != a.item.WorkspaceID {
				return dms.Invalid("a workflow comments in its own workspace")
			}
			c.Source, c.WorkflowID = commentstore.SourceWorkflow, r.WorkflowID
		}
		if c, err = t.DAL.Comments().Create(ctx, c); err != nil {
			return commentError(err)
		}
		if err = t.Record(ctx, subject, "comment.create", a.item, map[string]any{"comment_id": c.ID, "thread_id": c.ThreadID, "mentions": mentions}); err != nil {
			return err
		}
		if err = s.tell(ctx, t, a, c, mentions, !c.Root()); err != nil {
			return err
		}
		v, err := newViewer(ctx, t, subject, a, []commentstore.Comment{c}, nil)
		out = v.view(c)
		return err
	})
	return out, err
}

// Edit replaces the text of subject's comment; people it newly mentions
// are told.
func (s *Service) Edit(ctx context.Context, subject, id string, version int64, body string) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		c, a, err := s.load(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if err = a.refuse(); err != nil {
			return err
		}
		if c.Author != subject {
			return dms.ErrForbidden
		}
		if body, err = checkBody(body); err != nil {
			return err
		}
		mentions, err := mentionsOf(ctx, t, body, c.Mentions)
		if err != nil {
			return err
		}
		previous := c.Body
		c, added, err := t.DAL.Comments().Edit(ctx, id, version, body, mentions)
		if err != nil {
			return commentError(err)
		}
		if err = t.Record(ctx, subject, "comment.edit", a.item, map[string]any{"comment_id": c.ID, "previous": previous}); err != nil {
			return err
		}
		if err = s.tell(ctx, t, a, c, added, false); err != nil {
			return err
		}
		v, err := newViewer(ctx, t, subject, a, []commentstore.Comment{c}, nil)
		out = v.view(c)
		return err
	})
	return out, err
}

// Delete empties a comment: its author's or, for the item's managers, any.
func (s *Service) Delete(ctx context.Context, subject, id string, version int64) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		c, a, err := s.load(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if err = a.refuse(); err != nil {
			return err
		}
		if c.Author != subject && !a.manage {
			return dms.ErrForbidden
		}
		if _, err = t.DAL.Comments().Delete(ctx, id, version, subject); err != nil {
			return commentError(err)
		}
		return t.Record(ctx, subject, "comment.delete", a.item, map[string]any{"comment_id": c.ID, "author": c.Author, "body": c.Body})
	})
}

// Resolve marks a thread resolved, or open again: its author, people who
// may change the item, and its managers may.
func (s *Service) Resolve(ctx context.Context, subject, id string, version int64, resolved bool) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		c, a, err := s.load(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if err = a.refuse(); err != nil {
			return err
		}
		if c.Author != subject && !a.write && !a.manage {
			return dms.ErrForbidden
		}
		if c, err = t.DAL.Comments().Resolve(ctx, id, version, resolved, subject); err != nil {
			return commentError(err)
		}
		action := "comment.reopen"
		if resolved {
			action = "comment.resolve"
		}
		if err = t.Record(ctx, subject, action, a.item, map[string]any{"comment_id": c.ID}); err != nil {
			return err
		}
		v, err := newViewer(ctx, t, subject, a, []commentstore.Comment{c}, nil)
		out = v.view(c)
		return err
	})
	return out, err
}

func commentError(err error) error {
	switch {
	case dberr.IsNotFound(err):
		return dms.ErrNotFound
	case errors.Is(err, dberr.ErrConcurrentUpdate), errors.Is(err, dberr.ErrInvalidStateTransition):
		return fmt.Errorf("%w: %v", dms.ErrConflict, err)
	case errors.Is(err, dberr.ErrInvalidContainment), errors.Is(err, dberr.ErrCheckViolation):
		return dms.Invalid(strings.TrimPrefix(err.Error(), dberr.ErrInvalidContainment.Error()+": "))
	}
	return err
}

// tell notifies the people a comment mentions and, for a reply, the
// thread's other participants, folding a burst of replies into one unread
// notification each. Only people who may read the discussion are told, and
// never the author.
func (s *Service) tell(ctx context.Context, t *dms.Service, a access, c commentstore.Comment, mentioned []string, reply bool) error {
	if len(mentioned) == 0 && !reply {
		return nil
	}
	n, err := names(ctx, t, append([]string{c.Author}, mentioned...))
	if err != nil {
		return err
	}
	place, err := s.itemName(ctx, t, a, c.Author)
	if err != nil {
		return err
	}
	allowed := func(ctx context.Context, t *dms.Service, subject string) (bool, error) {
		d, err := s.discussion(ctx, t, subject, c.ItemID)
		if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
			return false, nil
		}
		return d.read, err
	}
	body, err := plain(ctx, t, c.Body)
	if err != nil {
		return err
	}
	base := notify.Message{WorkspaceID: a.item.WorkspaceID, Body: notify.Clip(body, notifications.MaxBody), ItemID: c.ItemID, CommentID: c.ID,
		Source: notifications.SourceUser, Sender: c.Author, WorkflowID: c.WorkflowID, Actor: c.Author, Allowed: allowed}
	if c.Source == commentstore.SourceWorkflow {
		base.Source = notifications.SourceWorkflow
	}
	if len(mentioned) > 0 {
		m := base
		m.Type, m.Title = notifications.TypeCommentMentioned, notify.Clip(n[c.Author]+" mentioned you on "+place, notifications.MaxTitle)
		m.DedupKey = "comment:" + c.ID + ":mention"
		if _, err = notify.Send(ctx, t, m, mentioned); err != nil {
			return err
		}
	}
	if !reply {
		return nil
	}
	root, err := t.DAL.Comments().Find(ctx, c.ThreadID)
	if err != nil {
		return err
	}
	replies, err := t.DAL.Comments().Replies(ctx, []string{c.ThreadID})
	if err != nil {
		return err
	}
	participants := []string{root.Author}
	for _, r := range replies {
		if r.DeletedAt == nil {
			participants = append(participants, r.Author)
		}
	}
	participants = slices.DeleteFunc(participants, func(p string) bool { return slices.Contains(mentioned, p) })
	m := base
	m.Type, m.Title = notifications.TypeCommentReplied, notify.Clip(n[c.Author]+" replied on "+place, notifications.MaxTitle)
	m.CoalesceKey = "comment:thread:" + c.ThreadID
	_, err = notify.Send(ctx, t, m, participants)
	return err
}

// itemName is the item's name that everyone told about its discussion can
// read: its draft's among collaborators; among readers its published
// revision's, or its draft's when it was never published, which only
// draft readers see.
//
// It reads as the author with the request's own credential, which just
// passed the discussion's checks: an integration's identity has no access
// but its token's.
func (s *Service) itemName(ctx context.Context, t *dms.Service, a access, author string) (string, error) {
	if a.mode.Mode == ModeReaders {
		if r, err := t.GetSurface(ctx, author, a.item.ID, "published"); err == nil {
			return r.Name, nil
		}
	}
	r, err := t.GetSurface(ctx, author, a.item.ID, "auto")
	if err != nil {
		return "", err
	}
	return r.Name, nil
}

// plain writes a comment's mentions as @Name, for notifications.
func plain(ctx context.Context, t *dms.Service, body string) (string, error) {
	n, err := names(ctx, t, Mentions(body))
	if err != nil {
		return "", err
	}
	return mentionPattern.ReplaceAllStringFunc(body, func(m string) string {
		return "@" + n[mentionPattern.FindStringSubmatch(m)[1]]
	}), nil
}

// Count is how many open and resolved threads an item's discussion has.
type Count struct {
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
}

// Counts returns the open and resolved thread counts of the items whose
// discussions subject may read; others are left out.
func (s *Service) Counts(ctx context.Context, subject string, itemIDs []string) (map[string]Count, error) {
	if len(itemIDs) > MaxCounted {
		return nil, dms.Invalid(fmt.Sprintf("item_ids names at most %d items", MaxCounted))
	}
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (map[string]Count, error) {
		var readable []string
		for _, id := range slices.Compact(slices.Sorted(slices.Values(itemIDs))) {
			a, err := s.discussion(ctx, t, subject, id)
			var invalid *dms.ValidationError
			if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) || errors.As(err, &invalid) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if a.read {
				readable = append(readable, id)
			}
		}
		counts, err := t.DAL.Comments().Counts(ctx, readable)
		if err != nil {
			return nil, err
		}
		out := make(map[string]Count, len(readable))
		for _, id := range readable {
			out[id] = Count(counts[id])
		}
		return out, nil
	})
}

// Candidate is someone a comment may mention.
type Candidate struct {
	Person
	// CanSee tells whether they may read the discussion; a mention never
	// shares the item.
	CanSee bool `json:"can_see"`
}

// MaxCandidates bounds the people Mentionable suggests.
const MaxCandidates = 20

// Mentionable suggests the people the item is shared with whose subject,
// name or email contains q, for a mention in the item's discussion.
func (s *Service) Mentionable(ctx context.Context, subject, itemID, q string) ([]Candidate, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Candidate, error) {
		a, err := s.discussion(ctx, t, subject, itemID)
		if err != nil {
			return nil, err
		}
		if err = a.refuse(); err != nil {
			return nil, err
		}
		people, err := sharedWith(ctx, t, a.item)
		if err != nil || len(people) == 0 {
			return []Candidate{}, err
		}
		rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: people})
		if err != nil {
			return nil, err
		}
		q = strings.ToLower(strings.TrimSpace(q))
		as := identity.WithoutAccessToken(ctx)
		out := []Candidate{}
		for _, u := range rows {
			if !u.Active || u.DeletedAt != nil || len(out) == MaxCandidates {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(u.ID), q) && !strings.Contains(strings.ToLower(u.DisplayName), q) && !strings.Contains(strings.ToLower(u.Email), q) {
				continue
			}
			name := u.DisplayName
			if name == "" {
				name = u.ID
			}
			d, err := s.discussion(as, t, u.ID, itemID)
			if err != nil && !errors.Is(err, dms.ErrForbidden) && !errors.Is(err, dms.ErrNotFound) {
				return nil, err
			}
			out = append(out, Candidate{Person: Person{Subject: u.ID, Name: name}, CanSee: err == nil && d.read})
		}
		return out, nil
	})
}

// sharedWith returns the active people the item is shared with: those
// granted access to it, its folders, its collection or its workspace, in
// person or through a group.
func sharedWith(ctx context.Context, t *dms.Service, item *resources.Resource) ([]string, error) {
	var entries []string
	for r := item; r != nil; {
		grants, err := t.DAL.Grants().List(ctx, grantstore.Filter{ResourceID: r.ID})
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			switch g.PrincipalType {
			case grantstore.PrincipalUser:
				entries = append(entries, g.PrincipalID)
			case grantstore.PrincipalGroup:
				entries = append(entries, "group:"+g.PrincipalID)
			}
		}
		if r.ParentID == nil || r.Kind == refs.KindWorkspace {
			break
		}
		if r, err = t.DAL.Resources().Resource(ctx, *r.ParentID); err != nil {
			return nil, err
		}
	}
	return recipients.People(ctx, t, slices.Compact(slices.Sorted(slices.Values(entries))))
}

// Settings is a workspace's or collection's comment setting.
type Settings struct {
	// Mode is the place's own mode; nil inherits.
	Mode      *Mode     `json:"mode"`
	Effective Effective `json:"effective"`
	Version   int64     `json:"version"`
}

// placeOf authorizes a workspace or collection for action.
func placeOf(ctx context.Context, t *dms.Service, subject, id, action string) (*resources.Resource, error) {
	r, err := t.Authorize(ctx, subject, id, action)
	if err != nil {
		return nil, err
	}
	if r.Kind != refs.KindWorkspace && r.Kind != refs.KindList && r.Kind != refs.KindLibrary {
		return nil, dms.Invalid("comment settings belong to workspaces, lists and libraries")
	}
	return r, nil
}

// PlaceSettings reads a workspace's or collection's comment setting.
func (s *Service) PlaceSettings(ctx context.Context, subject, id string) (Settings, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Settings, error) {
		r, err := placeOf(ctx, t, subject, id, "read")
		if err != nil {
			return Settings{}, err
		}
		return s.placeSettings(ctx, t, r)
	})
}

func (s *Service) placeSettings(ctx context.Context, t *dms.Service, r *resources.Resource) (Settings, error) {
	stored, found, err := t.DAL.Settings().Find(ctx, settings.KeyComments, r.ID)
	if err != nil {
		return Settings{}, err
	}
	var out Settings
	if found {
		var v placeValue
		if err = json.Unmarshal(stored.Value, &v); err != nil {
			return out, err
		}
		if v.Mode != "" {
			out.Mode = &v.Mode
		}
		out.Version = stored.Version
	}
	out.Effective, err = s.effective(ctx, t, r)
	return out, err
}

// SetPlace sets or, with nil, clears a workspace's or collection's mode;
// its managers may.
func (s *Service) SetPlace(ctx context.Context, subject, id string, version int64, mode *Mode) (Settings, error) {
	var out Settings
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		r, err := placeOf(ctx, t, subject, id, "manage")
		if err != nil {
			return err
		}
		v := placeValue{}
		if mode != nil {
			if !mode.Valid() {
				return dms.Invalid("mode is off, collaborators or readers")
			}
			v.Mode = *mode
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = t.DAL.Settings().Put(ctx, settings.KeyComments, id, raw, version, subject); err != nil {
			return settingError(err)
		}
		if err = t.Record(ctx, subject, "settings.comments", r, map[string]any{"mode": v.Mode}); err != nil {
			return err
		}
		out, err = s.placeSettings(ctx, t, r)
		return err
	})
	return out, err
}

func settingError(err error) error {
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return fmt.Errorf("%w: the settings changed; read them again", dms.ErrConflict)
	}
	return err
}

// OrganizationSettings is the organization's comment setting.
type OrganizationSettings struct {
	// Enabled is false when the organization turned comments off.
	Enabled bool `json:"enabled"`
	// Mode is the default mode; nil means collaborators.
	Mode *Mode `json:"mode"`
	// Available is false when the deployment runs without comments.
	Available bool  `json:"available"`
	Version   int64 `json:"version"`
}

// Organization reads the organization's comment setting.
func (s *Service) Organization(ctx context.Context) (OrganizationSettings, error) {
	stored, found, err := s.DMS.DAL.Settings().Find(ctx, settings.KeyComments, "")
	out := OrganizationSettings{Enabled: true, Available: s.Enabled}
	if err != nil || !found {
		return out, err
	}
	var v organizationValue
	if err = json.Unmarshal(stored.Value, &v); err != nil {
		return out, err
	}
	if v.Enabled != nil {
		out.Enabled = *v.Enabled
	}
	if v.Mode != "" {
		out.Mode = &v.Mode
	}
	out.Version = stored.Version
	return out, nil
}

// SetOrganization changes the organization's comment setting; it needs the
// organization.manage scope.
func (s *Service) SetOrganization(ctx context.Context, subject string, version int64, enabled bool, mode *Mode) (OrganizationSettings, error) {
	if err := identity.RequireScope(ctx, s.DMS.DAL, subject, "organization.manage"); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			return OrganizationSettings{}, dms.ErrForbidden
		}
		return OrganizationSettings{}, err
	}
	v := organizationValue{Enabled: &enabled}
	if mode != nil {
		if !mode.Valid() {
			return OrganizationSettings{}, dms.Invalid("mode is off, collaborators or readers")
		}
		v.Mode = *mode
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return OrganizationSettings{}, err
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if _, err := t.DAL.Settings().Put(ctx, settings.KeyComments, "", raw, version, subject); err != nil {
			return settingError(err)
		}
		return t.RecordEntity(ctx, subject, "settings.comments", dms.AuditTarget{}, map[string]any{"enabled": enabled, "mode": v.Mode})
	})
	if err != nil {
		return OrganizationSettings{}, err
	}
	return s.Organization(ctx)
}

// Visible reads a comment and reports whether subject may read its
// discussion, for live updates.
func (s *Service) Visible(ctx context.Context, subject, id string) (commentstore.Comment, bool, error) {
	type result struct {
		c  commentstore.Comment
		ok bool
	}
	r, err := dms.Read(ctx, s.DMS, func(t *dms.Service) (result, error) {
		c, _, err := s.load(ctx, t, subject, id)
		var invalid *dms.ValidationError
		if errors.Is(err, dms.ErrNotFound) || errors.As(err, &invalid) {
			return result{}, nil
		}
		return result{c, err == nil}, err
	})
	return r.c, r.ok, err
}
