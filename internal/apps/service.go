// Package apps is the apps feature: React screens that belong to a
// workspace, written as one TSX file the server compiles, with parameters
// chosen through a form and immutable versions. An app runs in a sandboxed
// frame of the web app and acts as the person viewing it.
package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/appcode"
	appstore "papergo/internal/dal/apps"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/script"
)

// Scope is the module scope writing apps needs, with manage access to the
// workspace.
const Scope = "app.manage"

// Service reads and writes the apps of workspaces.
type Service struct{ DMS *dms.Service }

// Save is an app as a create or replace request gives it. Key is given only
// on creation; without one it comes from the name.
type Save struct {
	Key         string         `json:"key,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Parameters  map[string]any `json:"parameters"`
	State       appstore.State `json:"state"`
}

// View is an app as the API shows it. Source is shown to those who may
// write apps.
type View struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	State       appstore.State `json:"state"`
	Parameters  map[string]any `json:"parameters"`
	// ParametersSchema is the current version's form; lists leave it out.
	ParametersSchema json.RawMessage `json:"parameters_schema,omitempty"`
	CurrentVersion   int             `json:"current_version"`
	// WorkspaceName is set in lists of every workspace's apps.
	WorkspaceName string    `json:"workspace_name,omitempty"`
	Source        string    `json:"source,omitempty"`
	CanWrite      bool      `json:"can_write"`
	CreatedBy     string    `json:"created_by"`
	UpdatedBy     string    `json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Version       int       `json:"version"`
}

// VersionView is one version of an app.
type VersionView struct {
	Number           int             `json:"number"`
	Source           string          `json:"source"`
	ParametersSchema json.RawMessage `json:"parameters_schema"`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
}

// Bundle is the compiled code of an app's current version, for its page.
type Bundle struct {
	Code, Hash string
	Number     int
}

var (
	keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	nonKey     = regexp.MustCompile(`[^a-z0-9]+`)
)

func keyFromName(name string) string {
	k := strings.Trim(nonKey.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(k) > 90 {
		k = strings.Trim(k[:90], "-")
	}
	if k == "" {
		k = "app"
	}
	return k
}

// denied is nil when err says only that subject has no access, and err when
// the answer could not be found out.
func denied(err error) error {
	if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
		return nil
	}
	return err
}

// writes reports whether subject may write the workspace's apps.
func writes(ctx context.Context, t *dms.Service, subject, workspaceID string) (bool, error) {
	if _, err := t.Authorize(ctx, subject, workspaceID, "manage"); err != nil {
		return false, denied(err)
	}
	if err := t.RequireScope(ctx, subject, Scope); err != nil {
		return false, denied(err)
	}
	return true, nil
}

// readable returns the app with id that subject may see, and whether they
// may write it: published apps for the workspace's readers, drafts for
// those who may write apps.
func readable(ctx context.Context, t *dms.Service, subject, id string) (appstore.App, bool, error) {
	a, err := t.DAL.Apps().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return appstore.App{}, false, dms.ErrNotFound
	}
	if err != nil {
		return appstore.App{}, false, err
	}
	if _, err = t.Authorize(ctx, subject, a.WorkspaceID, "read"); err != nil {
		if err = denied(err); err != nil {
			return appstore.App{}, false, err
		}
		return appstore.App{}, false, dms.ErrNotFound
	}
	write, err := writes(ctx, t, subject, a.WorkspaceID)
	if err != nil {
		return appstore.App{}, false, err
	}
	if a.State != appstore.StatePublished && !write {
		return appstore.App{}, false, dms.ErrNotFound
	}
	return a, write, nil
}

// creatable reports why subject may not add apps to a workspace, if so.
func creatable(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return err
	}
	write, err := writes(ctx, t, subject, workspaceID)
	if err != nil {
		return err
	}
	if !write {
		return dms.ErrForbidden
	}
	return nil
}

// writable returns the app with id that subject may write.
func writable(ctx context.Context, t *dms.Service, subject, id string) (appstore.App, error) {
	a, write, err := readable(ctx, t, subject, id)
	if err != nil {
		return appstore.App{}, err
	}
	if !write {
		return appstore.App{}, dms.ErrForbidden
	}
	return a, nil
}

func view(a appstore.App, v appstore.Version, write bool) View {
	out := View{ID: a.ID, WorkspaceID: a.WorkspaceID, Key: a.Key, Name: a.Name, Description: a.Description, State: a.State, Parameters: a.Parameters,
		ParametersSchema: v.ParametersSchema, CurrentVersion: a.CurrentVersion, CanWrite: write, CreatedBy: a.CreatedBy, UpdatedBy: a.UpdatedBy,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version}
	if write {
		out.Source = v.Source
	}
	return out
}

// List returns the apps of a workspace that subject may see, by name.
func (s *Service) List(ctx context.Context, subject, workspaceID string) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
			return nil, err
		}
		write, err := writes(ctx, t, subject, workspaceID)
		if err != nil {
			return nil, err
		}
		f := appstore.Filter{WorkspaceIDs: []string{workspaceID}}
		if !write {
			f.State = appstore.StatePublished
		}
		rows, err := t.DAL.Apps().List(ctx, f)
		if err != nil {
			return nil, err
		}
		out := make([]View, len(rows))
		for i, a := range rows {
			out[i] = summary(a, write)
		}
		return out, nil
	})
}

// summary is an app as lists show it: neither its source nor its parameters
// form, which belong to its version and which a list does not use.
func summary(a appstore.App, write bool) View {
	return view(a, appstore.Version{}, write)
}

// Launcher returns the apps subject may see in every workspace, by name,
// each with its workspace's name: published apps of the workspaces subject
// reads, and drafts too where subject writes apps. Its queries do not grow
// with the apps or the workspaces.
func (s *Service) Launcher(ctx context.Context, subject string) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		withApps, err := t.DAL.Apps().Workspaces(ctx)
		if err != nil {
			return nil, err
		}
		reads, err := t.AuthorizeMany(ctx, subject, "read", withApps)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(reads))
		for _, id := range withApps {
			if reads[id] {
				ids = append(ids, id)
			}
		}
		out := []View{}
		if len(ids) == 0 {
			return out, nil
		}
		manages := map[string]bool{}
		err = t.RequireScope(ctx, subject, Scope)
		if err == nil {
			if manages, err = t.AuthorizeMany(ctx, subject, "manage", ids); err != nil {
				return nil, err
			}
		} else if err = denied(err); err != nil {
			return nil, err
		}
		workspaces, err := t.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids, State: resources.Live})
		if err != nil {
			return nil, err
		}
		names := make(map[string]string, len(workspaces))
		for _, ws := range workspaces {
			names[ws.ID] = ws.Name
		}
		rows, err := t.DAL.Apps().List(ctx, appstore.Filter{WorkspaceIDs: ids})
		if err != nil {
			return nil, err
		}
		for _, a := range rows {
			write := manages[a.WorkspaceID]
			if a.State != appstore.StatePublished && !write {
				continue
			}
			item := summary(a, write)
			item.WorkspaceName = names[a.WorkspaceID]
			out = append(out, item)
		}
		return out, nil
	})
}

// Get returns one app that subject may see.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (View, error) {
		a, write, err := readable(ctx, t, subject, id)
		if err != nil {
			return View{}, err
		}
		v, err := t.DAL.Apps().Version(ctx, a.ID, int64(a.CurrentVersion))
		if err != nil {
			return View{}, err
		}
		return view(a, v, write), nil
	})
}

// Bundle returns the compiled code of an app subject may see.
func (s *Service) Bundle(ctx context.Context, subject, id string) (Bundle, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Bundle, error) {
		a, _, err := readable(ctx, t, subject, id)
		if err != nil {
			return Bundle{}, err
		}
		v, err := t.DAL.Apps().Version(ctx, a.ID, int64(a.CurrentVersion))
		if err != nil {
			return Bundle{}, err
		}
		return Bundle{Code: v.Bundle, Hash: v.BundleHash, Number: v.Number}, nil
	})
}

// Versions returns an app's versions, newest first, to those who may write
// it.
func (s *Service) Versions(ctx context.Context, subject, id string) ([]VersionView, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]VersionView, error) {
		if _, err := writable(ctx, t, subject, id); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Apps().Versions(ctx, id)
		if err != nil {
			return nil, err
		}
		out := make([]VersionView, len(rows))
		for i, v := range rows {
			out[i] = VersionView{Number: v.Number, Source: v.Source, ParametersSchema: v.ParametersSchema, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt}
		}
		return out, nil
	})
}

// compile checks an app's settings and compiles its source. It takes no
// database connection: running a source may take seconds, so it runs after
// authorizing and before the write's transaction, which would hold up every
// other write meanwhile.
func compile(ctx context.Context, in *Save) (appcode.Compiled, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 200 {
		return appcode.Compiled{}, dms.Invalid("name needs 1 to 200 characters")
	}
	if utf8.RuneCountInString(in.Description) > 2000 {
		return appcode.Compiled{}, dms.Invalid("description is limited to 2000 characters")
	}
	if in.State == "" {
		in.State = appstore.StateDraft
	}
	if !in.State.Valid() {
		return appcode.Compiled{}, dms.Invalid("state is draft or published")
	}
	compiled, err := appcode.Compile(ctx, in.Source)
	var problem *script.BundleError
	if errors.As(err, &problem) {
		return appcode.Compiled{}, dms.Invalid("the app does not compile:\n" + problem.Error())
	}
	return compiled, err
}

// prepare checks a compiled app's parameters against its form as subject:
// their types and picks always, and the form's required fields once the app
// is published.
func prepare(ctx context.Context, t *dms.Service, subject, workspaceID string, in *Save, compiled appcode.Compiled) (appstore.Content, error) {
	schema := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(compiled.ParametersSchema))
	dec.UseNumber()
	if err := dec.Decode(&schema); err != nil {
		return appstore.Content{}, dms.Invalid("parameters: " + err.Error())
	}
	schema = forms.Plain(schema).(map[string]any)
	if err := forms.Validate(schema, "parameters"); err != nil {
		return appstore.Content{}, dms.Invalid(err.Error())
	}
	if err := forms.CheckConfig(ctx, t, subject, workspaceID, schema, "parameters"); err != nil {
		return appstore.Content{}, err
	}
	check := schema
	if in.State != appstore.StatePublished {
		check = map[string]any{}
		for k, v := range schema {
			if k != "required" {
				check[k] = v
			}
		}
	}
	values, err := forms.Values(check, forms.Plain(orEmpty(in.Parameters)).(map[string]any), "parameters")
	if err != nil {
		if in.State == appstore.StatePublished {
			return appstore.Content{}, dms.Invalid(err.Error() + "; choose the app's parameters before publishing it")
		}
		return appstore.Content{}, dms.Invalid(err.Error())
	}
	if err = forms.CheckValues(ctx, t, subject, workspaceID, schema, values, "parameters"); err != nil {
		return appstore.Content{}, dms.Invalid(err.Error())
	}
	in.Parameters = values
	return appstore.Content{Source: in.Source, Bundle: compiled.Code, BundleHash: compiled.Hash, ParametersSchema: compiled.ParametersSchema}, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func audit(ctx context.Context, t *dms.Service, subject, action string, a appstore.App, details map[string]any) error {
	details["app_id"] = a.ID
	details["key"] = a.Key
	return t.RecordEntity(ctx, subject, action, dms.AuditTarget{WorkspaceID: a.WorkspaceID, ResourceID: a.WorkspaceID, Kind: "workspace"}, details)
}

func conflict(err error, what string) error {
	if errors.Is(err, dberr.ErrUniqueViolation) {
		return fmt.Errorf("%w: %s", dms.ErrConflict, what)
	}
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return dms.ErrPreconditionFailed
	}
	return err
}

// Create adds an app to a workspace as subject.
func (s *Service) Create(ctx context.Context, subject, workspaceID string, in Save) (View, error) {
	if _, err := dms.Read(ctx, s.DMS, func(t *dms.Service) (struct{}, error) {
		return struct{}{}, creatable(ctx, t, subject, workspaceID)
	}); err != nil {
		return View{}, err
	}
	compiled, err := compile(ctx, &in)
	if err != nil {
		return View{}, err
	}
	var out View
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := creatable(ctx, t, subject, workspaceID); err != nil {
			return err
		}
		content, err := prepare(ctx, t, subject, workspaceID, &in, compiled)
		if err != nil {
			return err
		}
		key := in.Key
		if key != "" {
			if !keyPattern.MatchString(key) || len(key) > 100 {
				return dms.Invalid("key must be lowercase letters, digits, '-' or '_' (at most 100)")
			}
		} else if key, err = freeKey(ctx, t, workspaceID, keyFromName(in.Name)); err != nil {
			return err
		}
		a, err := t.DAL.Apps().Create(ctx, appstore.CreateInput{WorkspaceID: workspaceID, Key: key, Name: in.Name, Description: in.Description,
			State: in.State, Parameters: in.Parameters, Content: content, CreatedBy: subject})
		if err != nil {
			return conflict(err, "another app of the workspace has this key or name")
		}
		if err = audit(ctx, t, subject, "app.create", a, map[string]any{"version": 1, "state": string(a.State)}); err != nil {
			return err
		}
		out = view(a, appstore.Version{Number: 1, Content: content}, true)
		return nil
	})
	return out, err
}

func freeKey(ctx context.Context, t *dms.Service, workspaceID, base string) (string, error) {
	key := base
	for n := 2; ; n++ {
		taken, err := t.DAL.Apps().List(ctx, appstore.Filter{WorkspaceIDs: []string{workspaceID}, Key: key})
		if err != nil || len(taken) == 0 {
			return key, err
		}
		key = fmt.Sprintf("%s-%d", base, n)
	}
}

// Update replaces an app read at version: its name, description, state,
// parameters and source. A source that changes becomes the next version.
func (s *Service) Update(ctx context.Context, subject, id string, version int, in Save) (View, error) {
	if _, err := dms.Read(ctx, s.DMS, func(t *dms.Service) (appstore.App, error) { return writable(ctx, t, subject, id) }); err != nil {
		return View{}, err
	}
	compiled, err := compile(ctx, &in)
	if err != nil {
		return View{}, err
	}
	var out View
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		a, err := writable(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if in.Key != "" && in.Key != a.Key {
			return dms.Invalid("an app's key does not change")
		}
		content, err := prepare(ctx, t, subject, a.WorkspaceID, &in, compiled)
		if err != nil {
			return err
		}
		a, v, err := t.DAL.Apps().Revise(ctx, id, version, subject, &content, appstore.Changes{Name: &in.Name, Description: &in.Description, State: &in.State, Parameters: &in.Parameters})
		if err != nil {
			return conflict(err, "another app of the workspace has this name")
		}
		if err = audit(ctx, t, subject, "app.update", a, map[string]any{"version": v.Number, "state": string(a.State)}); err != nil {
			return err
		}
		out = view(a, v, true)
		return nil
	})
	return out, err
}

// Delete deletes an app read at version, with its versions.
func (s *Service) Delete(ctx context.Context, subject, id string, version int) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		a, err := writable(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if err = t.DAL.Apps().Delete(ctx, id, version); err != nil {
			return conflict(err, "")
		}
		return audit(ctx, t, subject, "app.delete", a, map[string]any{})
	})
}
