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
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	// ItemID, TaskID or RunID is what the notification opens.
	ItemID, TaskID, RunID string
	Source                notifications.Source
	// Sender is who sent the message or caused what it reports, and
	// WorkflowID the workflow that sent it.
	Sender, WorkflowID string
	// DedupKey sends the message to each person once: a later send with
	// the same key reaches only people who do not have it yet.
	DedupKey string
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
			ItemID: m.ItemID, TaskID: m.TaskID, RunID: m.RunID, Source: m.Source, Sender: m.Sender, WorkflowID: m.WorkflowID, DedupKey: m.DedupKey,
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

// canRead lets people receive m who can read its item or, without one, its
// workspace.
func canRead(m Message) func(context.Context, *dms.Service, string) (bool, error) {
	return func(ctx context.Context, t *dms.Service, subject string) (bool, error) {
		target := m.ItemID
		if target == "" {
			target = m.WorkspaceID
		}
		if target == "" {
			return true, nil
		}
		return Can(ctx, t, subject, target, "read")
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
	ID          string               `json:"id"`
	Type        notifications.Type   `json:"type"`
	Title       string               `json:"title"`
	Body        string               `json:"body,omitempty"`
	WorkspaceID *string              `json:"workspace_id,omitempty"`
	ItemID      *string              `json:"item_id,omitempty"`
	TaskID      *string              `json:"task_id,omitempty"`
	RunID       *string              `json:"run_id,omitempty"`
	Source      notifications.Source `json:"source"`
	Sender      *string              `json:"sender,omitempty"`
	WorkflowID  *string              `json:"workflow_id,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	ReadAt      *time.Time           `json:"read_at,omitempty"`
}

func viewOf(n notifications.Notification) View {
	return View{ID: n.ID, Type: n.Type, Title: n.Title, Body: n.Body, WorkspaceID: ptr(n.WorkspaceID), ItemID: ptr(n.ItemID), TaskID: ptr(n.TaskID),
		RunID: ptr(n.RunID), Source: n.Source, Sender: ptr(n.Sender), WorkflowID: ptr(n.WorkflowID), CreatedAt: n.CreatedAt, ReadAt: n.ReadAt}
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

// Inbox lists subject's notifications, newest first.
func (s *Service) Inbox(ctx context.Context, subject string, f Filter) (dms.Page[View], error) {
	if f.Type != "" && !f.Type.Valid() {
		return dms.Page[View]{}, dms.Invalid("type is message, task.assigned, task.completed or workflow.failed")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := 0
	if f.After != "" {
		n, err := strconv.Atoi(f.After)
		if err != nil || n < 0 {
			return dms.Page[View]{}, dms.Invalid("$skiptoken is not a cursor of this list")
		}
		offset = n
	}
	rows, err := s.DMS.DAL.Notifications().List(ctx, notifications.Filter{Recipient: subject, Unread: f.Unread, Type: f.Type, WorkspaceID: f.WorkspaceID, Offset: offset, Limit: limit + 1})
	if err != nil {
		return dms.Page[View]{}, err
	}
	out := dms.Page[View]{Data: []View{}}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextCursor = strconv.Itoa(offset + limit)
	}
	for _, n := range rows {
		out.Data = append(out.Data, viewOf(n))
	}
	return out, nil
}

// Get reads one of subject's notifications; others' are not found.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	n, err := s.DMS.DAL.Notifications().Find(ctx, id)
	if dberr.IsNotFound(err) || (err == nil && n.Recipient != subject) {
		return View{}, dms.ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	return viewOf(n), nil
}

// Unread counts subject's unread notifications.
func (s *Service) Unread(ctx context.Context, subject string) (int64, error) {
	return s.DMS.DAL.Notifications().Unread(ctx, subject)
}

// MaxMarked bounds the notifications one request names.
const MaxMarked = 500

// MarkRead marks subject's notifications read: those of ids, or with none
// every one created at or before before, or else all. It returns how many
// it marked.
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
	var marked []string
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		var err error
		marked, err = t.DAL.Notifications().MarkRead(ctx, subject, ids, until)
		return err
	})
	return len(marked), err
}

// Delete removes one of subject's notifications.
func (s *Service) Delete(ctx context.Context, subject, id string) error {
	err := s.DMS.Write(ctx, func(t *dms.Service) error { return t.DAL.Notifications().Delete(ctx, subject, id) })
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	return err
}

// Send sends a message from subject, or from the workflow run that makes
// the request, to the people its recipient entries name who can read its
// item or else its workspace. Naming someone sends to them even when they
// are the sender.
func (s *Service) Send(ctx context.Context, subject, run string, in SendInput) (Result, error) {
	var out Result
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
		m := Message{Type: notifications.TypeMessage, WorkspaceID: in.WorkspaceID, Title: in.Title, Body: in.Body, ItemID: in.ItemID,
			Source: notifications.SourceUser, Sender: subject, DedupKey: in.DedupKey}
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
