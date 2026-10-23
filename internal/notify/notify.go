// Package notify is PaperGo's notification building block: it tells
// people that something happened, where tasks ask them to act. A
// notification is one message to one person, written to their inbox in the
// transaction of the change it reports and shown live to their streams.
//
// Nobody is told about content they cannot read now, nobody is told about
// their own action, and a dedup key sends a message to a person once. A
// notification starts no workflow and raises no domain event, so
// notifications cannot loop.
package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/recipients"
)

// MaxRecipients bounds the people one send reaches, after groups and roles
// are expanded.
const MaxRecipients = 500

// Message is what Send tells each recipient.
type Message struct {
	Type        notifications.Type
	WorkspaceID string
	Title, Body string
	// ItemID, TaskID or RunID is what the notification opens; CommentID
	// opens a comment's thread on ItemID.
	ItemID, TaskID, RunID, CommentID string
	Source                           notifications.Source
	// Sender is who sent the message or caused what it reports, and
	// WorkflowID the workflow that sent it.
	Sender, WorkflowID string
	// DedupKey sends the message to each person once: a later send with
	// the same key reaches only people who do not have it yet.
	DedupKey string
	// CoalesceKey folds the message into a person's unread notification
	// with the key, which counts one more, instead of adding another.
	CoalesceKey string
	// Actor caused what the message reports and is not told about it.
	Actor string
	// Allowed reports whether a person may receive the message. Without it,
	// a person must be able to read the linked item, or else the workspace.
	Allowed func(ctx context.Context, t *dms.Service, subject string) (bool, error)
}

// Result counts what a send did: notifications written, people left out
// because they cannot read what it is about, and people who already had
// one with its dedup key.
type Result struct {
	Sent         int `json:"sent"`
	Skipped      int `json:"skipped"`
	Deduplicated int `json:"deduplicated"`
}

// Send writes m to the inbox of each person in people inside t's write
// transaction. People are user subjects; resolve recipient entries with
// package recipients first.
func Send(ctx context.Context, t *dms.Service, m Message, people []string) (Result, error) {
	var out Result
	m.Title, m.Body, m.DedupKey = strings.TrimSpace(m.Title), strings.TrimSpace(m.Body), strings.TrimSpace(m.DedupKey)
	if m.Title == "" || utf8.RuneCountInString(m.Title) > notifications.MaxTitle {
		return out, dms.Invalid(fmt.Sprintf("a notification needs a title of up to %d characters", notifications.MaxTitle))
	}
	if utf8.RuneCountInString(m.Body) > notifications.MaxBody {
		return out, dms.Invalid(fmt.Sprintf("a notification's message has at most %d characters", notifications.MaxBody))
	}
	if utf8.RuneCountInString(m.DedupKey) > notifications.MaxDedupKey {
		return out, dms.Invalid(fmt.Sprintf("dedup_key has at most %d characters", notifications.MaxDedupKey))
	}
	people = slices.Compact(slices.Sorted(slices.Values(people)))
	if len(people) > MaxRecipients {
		return out, dms.Invalid(fmt.Sprintf("a notification reaches at most %d people", MaxRecipients))
	}
	allowed := m.Allowed
	if allowed == nil {
		allowed = canRead(m)
	}
	// Each person's own access decides, not the limits of the sender's
	// access token.
	as := identity.WithoutAccessToken(ctx)
	for _, person := range people {
		if person == "" || person == m.Actor {
			continue
		}
		ok, err := allowed(as, t, person)
		if err != nil {
			return out, err
		}
		if !ok {
			out.Skipped++
			continue
		}
		_, created, err := t.DAL.Notifications().Create(ctx, notifications.Notification{
			Recipient: person, WorkspaceID: m.WorkspaceID, Type: m.Type, Title: m.Title, Body: m.Body,
			ItemID: m.ItemID, TaskID: m.TaskID, RunID: m.RunID, CommentID: m.CommentID, Source: m.Source, Sender: m.Sender, WorkflowID: m.WorkflowID,
			DedupKey: m.DedupKey, CoalesceKey: m.CoalesceKey,
		})
		if err != nil {
			return out, err
		}
		if created {
			out.Sent++
		} else {
			out.Deduplicated++
		}
	}
	return out, nil
}

// canRead lets people receive m who can read a revision of its item or,
// without one, its workspace.
func canRead(m Message) func(context.Context, *dms.Service, string) (bool, error) {
	return func(ctx context.Context, t *dms.Service, subject string) (bool, error) {
		if m.ItemID != "" {
			return CanSee(ctx, t, subject, m.ItemID)
		}
		if m.WorkspaceID == "" {
			return true, nil
		}
		return Can(ctx, t, subject, m.WorkspaceID, "read")
	}
}

// Can reports whether subject may perform action on the resource, treating
// a refusal or a missing resource as no.
func Can(ctx context.Context, t *dms.Service, subject, id, action string) (bool, error) {
	_, err := t.Authorize(ctx, subject, id, action)
	if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CanSee reports whether subject can read a revision of the item: its
// draft, or its published revision. Read access to an item that was never
// published shows a reader nothing.
func CanSee(ctx context.Context, t *dms.Service, subject, itemID string) (bool, error) {
	_, err := t.GetSurface(ctx, subject, itemID, "auto")
	if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Clip shortens text to at most max characters, ending a shortened text
// with an ellipsis, so text from elsewhere fits a notification.
func Clip(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max-1]) + "…"
}

// SendInput is a message people and workflows send through the API.
type SendInput struct {
	WorkspaceID string
	// Recipients are recipient entries, as tasks take them.
	Recipients  []string
	Title, Body string
	ItemID      string
	DedupKey    string
}

// View is a notification as the API shows it.
type View struct {
	ID          string             `json:"id"`
	Type        notifications.Type `json:"type"`
	Title       string             `json:"title"`
	Body        string             `json:"body,omitempty"`
	WorkspaceID *string            `json:"workspace_id,omitempty"`
	ItemID      *string            `json:"item_id,omitempty"`
	TaskID      *string            `json:"task_id,omitempty"`
	RunID       *string            `json:"run_id,omitempty"`
	CommentID   *string            `json:"comment_id,omitempty"`
	// Count is how many notifications this one stands for, such as
	// replies to a thread while it was unread.
	Count      int64                `json:"count"`
	Source     notifications.Source `json:"source"`
	Sender     *string              `json:"sender,omitempty"`
	WorkflowID *string              `json:"workflow_id,omitempty"`
	CreatedAt  time.Time            `json:"created_at"`
	ReadAt     *time.Time           `json:"read_at,omitempty"`
}

func viewOf(n notifications.Notification) View {
	return View{ID: n.ID, Type: n.Type, Title: n.Title, Body: n.Body, WorkspaceID: ptr(n.WorkspaceID), ItemID: ptr(n.ItemID), TaskID: ptr(n.TaskID),
		RunID: ptr(n.RunID), CommentID: ptr(n.CommentID), Count: n.Count, Source: n.Source, Sender: ptr(n.Sender), WorkflowID: ptr(n.WorkflowID), CreatedAt: n.CreatedAt, ReadAt: n.ReadAt}
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Filter selects a person's notifications, newest first.
type Filter struct {
	Unread      bool
	Type        notifications.Type
	WorkspaceID string
	Limit       int
	// After continues from a previous page's NextCursor.
	After string
}

// Service reads and writes people's inboxes for the API, each call in its
// own transaction.
type Service struct {
	DMS *dms.Service
}

// scanBatch is how many notifications a read takes at once while it skips
// those an access token may not show, and maxScanned how many a page or a
// count looks at in all.
const (
	scanBatch  = 200
	maxScanned = 2000
)

// Inbox lists subject's notifications, newest first. A page continues
// below the last notification of the previous one, so notifications read
// or deleted meanwhile do not shift it.
func (s *Service) Inbox(ctx context.Context, subject string, f Filter) (dms.Page[View], error) {
	if f.Type != "" && !f.Type.Valid() {
		return dms.Page[View]{}, dms.Invalid("type is message, task.assigned, task.completed, workflow.failed, comment.mentioned or comment.replied")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := notifications.Filter{Recipient: subject, Unread: f.Unread, Type: f.Type, WorkspaceID: f.WorkspaceID}
	if f.After != "" {
		key, err := parseCursor(f.After)
		if err != nil {
			return dms.Page[View]{}, err
		}
		q.After = &key
	}
	out := dms.Page[View]{Data: []View{}}
	for scanned := 0; ; {
		q.Limit = limit + 1 - len(out.Data)
		if limited(ctx) {
			q.Limit = scanBatch
		}
		rows, err := s.DMS.DAL.Notifications().List(ctx, q)
		if err != nil {
			return out, err
		}
		show, err := s.visible(ctx, subject, rows)
		if err != nil {
			return out, err
		}
		for _, n := range rows {
			scanned++
			if !show[n.ID] {
				q.After = &notifications.Key{CreatedAt: n.CreatedAt, ID: n.ID}
				continue
			}
			if len(out.Data) == limit {
				out.NextCursor = cursorOf(out.Data[len(out.Data)-1])
				return out, nil
			}
			out.Data = append(out.Data, viewOf(n))
			q.After = &notifications.Key{CreatedAt: n.CreatedAt, ID: n.ID}
		}
		if len(rows) < q.Limit {
			return out, nil
		}
		if scanned >= maxScanned {
			// A page cut short still links to the rest.
			out.NextCursor = cursorOf(View{ID: q.After.ID, CreatedAt: q.After.CreatedAt})
			return out, nil
		}
	}
}

// cursorOf names the place after which the next page starts.
func cursorOf(v View) string {
	return base64.RawURLEncoding.EncodeToString([]byte(v.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + v.ID))
}

func parseCursor(cursor string) (notifications.Key, error) {
	invalid := dms.Invalid("$skiptoken is not a cursor of this list")
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return notifications.Key{}, invalid
	}
	at, id, ok := strings.Cut(string(raw), "|")
	created, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || id == "" {
		return notifications.Key{}, invalid
	}
	return notifications.Key{CreatedAt: created, ID: id}, nil
}

// limited reports whether the request carries an access token, whose scopes
// and places limit which notifications it may show.
func limited(ctx context.Context) bool {
	_, ok := identity.AccessTokenContext(ctx)
	return ok
}

// visible reports which of subject's notifications the request may show.
// Signed in, a person sees all of their own. An access token sees those
// about content it may read: the linked item, a task's item, or else the
// workspace, which a token limited to places never sees on its own, as
// such a notification may concern anything there.
func (s *Service) visible(ctx context.Context, subject string, rows []notifications.Notification) (map[string]bool, error) {
	out := map[string]bool{}
	token, ok := identity.AccessTokenContext(ctx)
	for _, n := range rows {
		out[n.ID] = !ok
	}
	if !ok {
		return out, nil
	}
	for _, n := range rows {
		item := n.ItemID
		if n.TaskID != "" {
			tk, err := s.DMS.DAL.Tasks().Find(ctx, n.TaskID)
			if err != nil && !dberr.IsNotFound(err) {
				return nil, err
			}
			item = tk.ItemID
		}
		var err error
		switch {
		case item != "":
			out[n.ID], err = Can(ctx, s.DMS, subject, item, "read")
		case len(token.Resources) > 0 || n.WorkspaceID == "":
			out[n.ID] = false
		default:
			out[n.ID], err = Can(ctx, s.DMS, subject, n.WorkspaceID, "read")
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// find reads one of subject's notifications that the request may show;
// others are not found.
func (s *Service) find(ctx context.Context, subject, id string) (notifications.Notification, error) {
	n, err := s.DMS.DAL.Notifications().Find(ctx, id)
	if dberr.IsNotFound(err) || (err == nil && n.Recipient != subject) {
		return n, dms.ErrNotFound
	}
	if err != nil {
		return n, err
	}
	show, err := s.visible(ctx, subject, []notifications.Notification{n})
	if err != nil {
		return n, err
	}
	if !show[n.ID] {
		return n, dms.ErrNotFound
	}
	return n, nil
}

// Get reads one of subject's notifications; others' are not found.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	n, err := s.find(ctx, subject, id)
	if err != nil {
		return View{}, err
	}
	return viewOf(n), nil
}

// Unread counts subject's unread notifications that the request may show.
// Through an access token the count stops at the notifications among the
// latest 2,000 unread ones.
func (s *Service) Unread(ctx context.Context, subject string) (int64, error) {
	if !limited(ctx) {
		return s.DMS.DAL.Notifications().Unread(ctx, subject)
	}
	ids, err := s.unread(ctx, subject, time.Time{})
	return int64(len(ids)), err
}

// unread returns the IDs of subject's unread notifications created until
// then (any time when zero) that the request may show, among the latest
// 2,000.
func (s *Service) unread(ctx context.Context, subject string, until time.Time) ([]string, error) {
	q := notifications.Filter{Recipient: subject, Unread: true, Until: until, Limit: scanBatch}
	var ids []string
	for scanned := 0; scanned < maxScanned; {
		rows, err := s.DMS.DAL.Notifications().List(ctx, q)
		if err != nil {
			return nil, err
		}
		show, err := s.visible(ctx, subject, rows)
		if err != nil {
			return nil, err
		}
		for _, n := range rows {
			if show[n.ID] {
				ids = append(ids, n.ID)
			}
			q.After = &notifications.Key{CreatedAt: n.CreatedAt, ID: n.ID}
		}
		scanned += len(rows)
		if len(rows) < scanBatch {
			break
		}
	}
	return ids, nil
}

// MaxMarked bounds the notifications one request names.
const MaxMarked = 500

// MarkRead marks subject's notifications read: those of ids, or with none
// every one created at or before before, or else all. An access token
// marks only those it may show. It returns how many it marked.
func (s *Service) MarkRead(ctx context.Context, subject string, ids []string, before *time.Time) (int, error) {
	if len(ids) > MaxMarked {
		return 0, dms.Invalid(fmt.Sprintf("ids names at most %d notifications", MaxMarked))
	}
	if len(ids) > 0 && before != nil {
		return 0, dms.Invalid("pass ids or before, not both")
	}
	until := time.Now()
	if before != nil {
		until = *before
	}
	if limited(ctx) {
		var shown []string
		if len(ids) == 0 {
			var err error
			if shown, err = s.unread(ctx, subject, until); err != nil {
				return 0, err
			}
		}
		for _, id := range ids {
			if _, err := s.find(ctx, subject, id); err == nil {
				shown = append(shown, id)
			} else if !errors.Is(err, dms.ErrNotFound) {
				return 0, err
			}
		}
		if len(shown) == 0 {
			return 0, nil
		}
		ids = shown
	}
	marked := 0
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		if len(ids) == 0 {
			done, err := t.DAL.Notifications().MarkRead(ctx, subject, nil, until)
			marked = len(done)
			return err
		}
		for chunk := range slices.Chunk(ids, MaxMarked) {
			done, err := t.DAL.Notifications().MarkRead(ctx, subject, chunk, until)
			if err != nil {
				return err
			}
			marked += len(done)
		}
		return nil
	})
	return marked, err
}

// Delete removes one of subject's notifications.
func (s *Service) Delete(ctx context.Context, subject, id string) error {
	if _, err := s.find(ctx, subject, id); err != nil {
		return err
	}
	err := s.DMS.Write(ctx, func(t *dms.Service) error { return t.DAL.Notifications().Delete(ctx, subject, id) })
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	return err
}

// Send sends a message from subject, or from the workflow run that makes
// the request, to the people its recipient entries name who can read its
// item or else its workspace. Naming someone sends to them even when they
// are the sender. Sending writes to other people's inboxes, so it needs the
// item.write scope. A caller's dedup key never meets the keys PaperGo uses
// for its own notifications.
func (s *Service) Send(ctx context.Context, subject, run string, in SendInput) (Result, error) {
	var out Result
	if err := identity.RequireScope(ctx, s.DMS.DAL, subject, "item.write"); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			return out, dms.ErrForbidden
		}
		return out, err
	}
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		ws, err := t.Authorize(ctx, subject, in.WorkspaceID, "read")
		if err != nil {
			return err
		}
		if ws.Kind != "workspace" {
			return dms.Invalid("the notification's workspace is not a workspace")
		}
		if in.ItemID != "" {
			item, err := t.Authorize(ctx, subject, in.ItemID, "read")
			if err != nil {
				return err
			}
			if item.Kind != "item" || item.WorkspaceID != in.WorkspaceID {
				return dms.Invalid("item_id must be an item of the workspace")
			}
		}
		if utf8.RuneCountInString(in.DedupKey) > notifications.MaxDedupKey-len(messageKeys) {
			return dms.Invalid(fmt.Sprintf("dedup_key has at most %d characters", notifications.MaxDedupKey-len(messageKeys)))
		}
		m := Message{Type: notifications.TypeMessage, WorkspaceID: in.WorkspaceID, Title: in.Title, Body: in.Body, ItemID: in.ItemID,
			Source: notifications.SourceUser, Sender: subject}
		if key := strings.TrimSpace(in.DedupKey); key != "" {
			m.DedupKey = messageKeys + key
		}
		if run != "" {
			r, err := t.DAL.Runs().Find(ctx, run)
			if err != nil {
				return err
			}
			if r.WorkspaceID != in.WorkspaceID {
				return dms.Invalid("a workflow sends notifications in its own workspace")
			}
			m.Source, m.WorkflowID = notifications.SourceWorkflow, r.WorkflowID
		}
		if len(in.Recipients) == 0 {
			return dms.Invalid("recipients names who is told")
		}
		entries, err := recipients.Resolve(ctx, t, subject, in.Recipients, in.WorkspaceID, in.ItemID, subject)
		if err != nil {
			return err
		}
		people, err := recipients.People(ctx, t, entries)
		if err != nil {
			return err
		}
		if out, err = Send(ctx, t, m, people); err != nil {
			return err
		}
		return t.Record(ctx, subject, "notification.send", ws, map[string]any{"item_id": in.ItemID, "recipients": entries, "sent": out.Sent})
	})
	return out, err
}

// messageKeys prefixes the dedup keys of messages people and workflows
// send, apart from the keys of PaperGo's own notifications.
const messageKeys = "message:"
