package connections

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/integration"
	"papergo/internal/workflow"

	store "papergo/internal/dal/connections"
)

// Service reads and writes the connections of workspaces and holds them
// open. One Service serves the process: its runtime keeps the broker
// sessions and HTTP clients of every enabled connection.
type Service struct {
	// DMS is the service outside any transaction, which the runtime reads
	// with.
	DMS     *dms.Service
	Secrets *integration.Secrets
	// Workflows finds the connection.message triggers messages fire.
	Workflows *workflow.Service
	runtime   *Runtime
}

// TLS is a connection's TLS options.
type TLS struct {
	// CA is PEM certificates trusted for the target, beside the system's.
	CA string `json:"ca,omitempty"`
}

// Save is a connection as a create or replace request gives it. Key and
// kind are given on creation only. Credentials and the signing secret are
// kept when a replace leaves them out.
type Save struct {
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Key         string            `json:"key,omitempty"`
	Kind        Kind              `json:"kind,omitempty"`
	Name        string            `json:"name"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Target      string            `json:"target"`
	TLS         *TLS              `json:"tls,omitempty"`
	Credentials *Credentials      `json:"credentials,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Sign        bool              `json:"sign,omitempty"`
	HealthPath  string            `json:"health_path,omitempty"`
	Channels    []Channel         `json:"channels"`
}

// View is a connection as its managers see it: where it goes and how it
// signs in, without any secret.
type View struct {
	ID          string            `json:"id"`
	WorkspaceID string            `json:"workspace_id"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Kind        Kind              `json:"kind"`
	Enabled     bool              `json:"enabled"`
	Target      string            `json:"target"`
	TLS         TLS               `json:"tls"`
	Credentials Credentials       `json:"credentials"`
	Headers     map[string]string `json:"headers,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Sign        bool              `json:"sign,omitempty"`
	HealthPath  string            `json:"health_path,omitempty"`
	Channels    []Channel         `json:"channels"`
	Status      Status            `json:"status"`
	// SigningSecret is shown once: when an HTTP connection is created and
	// when a new one is made.
	SigningSecret string    `json:"signing_secret,omitempty"`
	CreatedBy     string    `json:"created_by"`
	UpdatedBy     string    `json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Version       int       `json:"version"`
}

// Use is a connection as apps and workflows see it: its key, kind, whether
// it works and its channels with what the caller may do with each. It
// names no target and no credential.
type UseView struct {
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	Kind      Kind             `json:"kind"`
	Connected bool             `json:"connected"`
	Channels  []ChannelUseView `json:"channels"`
}

// ChannelUseView is a channel as a caller sees it.
type ChannelUseView struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Topics  string   `json:"topics,omitempty"`
	Path    string   `json:"path,omitempty"`
	Methods []string `json:"methods,omitempty"`
	Retain  bool     `json:"retain,omitempty"`
	Uses    []Use    `json:"uses"`
	// May are the uses the caller may make of it now.
	May []Use `json:"may"`
}

// denied is nil when err says only that subject has no access, and err when
// the answer could not be found out.
func denied(err error) error {
	if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
		return nil
	}
	return err
}

// manages reports whether subject manages the workspace's connections.
func manages(ctx context.Context, t *dms.Service, subject, workspaceID string) (bool, error) {
	if _, err := t.Authorize(ctx, subject, workspaceID, "manage"); err != nil {
		return false, denied(err)
	}
	if err := t.RequireScope(ctx, subject, ManageScope); err != nil {
		return false, denied(err)
	}
	return true, nil
}

// requireManage refuses subject unless they manage the workspace's
// connections. Readers who do not are told they may not; others that the
// workspace does not exist.
func requireManage(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return err
	}
	ok, err := manages(ctx, t, subject, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return dms.ErrForbidden
	}
	return nil
}

// managed returns the connection with id that subject manages.
func managed(ctx context.Context, t *dms.Service, subject, id string) (store.Connection, error) {
	c, err := t.DAL.Connections().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return store.Connection{}, dms.ErrNotFound
	}
	if err != nil {
		return store.Connection{}, err
	}
	if err = requireManage(ctx, t, subject, c.WorkspaceID); err != nil {
		if errors.Is(err, dms.ErrForbidden) {
			return store.Connection{}, dms.ErrNotFound
		}
		return store.Connection{}, err
	}
	return c, nil
}

// Problems PaperGo answers for its own refusals, apart from what an
// endpoint answers.
func forbidden(detail string) error {
	return &httpapi.Problem{Status: 403, Code: "connection_forbidden", Detail: detail}
}

func accessMissing(channel string) error {
	return &httpapi.Problem{Status: 403, Code: "connection_access_missing", Detail: "the list channel " + channel + " borrows its access from no longer exists; a manager chooses another"}
}

func pathInvalid(detail string) error {
	return &httpapi.Problem{Status: 422, Code: "connection_path_invalid", Detail: detail}
}

// May says why subject may not use the channel this way, or nil when they
// may. Control needs the connection.control scope. Owners of the
// workspace may always; others need the channel's level on the resource it
// borrows from.
func May(ctx context.Context, t *dms.Service, subject string, c store.Connection, ch Channel, use Use) error {
	if !ch.Can(use) {
		return forbidden(fmt.Sprintf("channel %s does not %s", ch.Key, use))
	}
	if use == UseControl {
		if err := t.RequireScope(ctx, subject, ControlScope); err != nil {
			if denied(err) == nil {
				return forbidden("controlling through a connection needs the connection.control scope")
			}
			return err
		}
	}
	if _, err := t.Authorize(ctx, subject, c.WorkspaceID, "manage"); err == nil {
		return nil
	} else if denied(err) != nil {
		return err
	}
	a := ch.Access[use]
	if a == nil {
		a = &defaultAccess
	}
	on := a.On
	if on == OnWorkspace {
		on = c.WorkspaceID
	} else if err := borrowable(ctx, t, c.WorkspaceID, on); err != nil {
		if denied(err) == nil {
			return accessMissing(ch.Key)
		}
		return err
	}
	if _, err := t.Authorize(ctx, subject, on, string(a.Level)); err != nil {
		if denied(err) == nil {
			return forbidden(fmt.Sprintf("you may not %s through channel %s", use, ch.Key))
		}
		return err
	}
	return nil
}

// borrowable says why id is not a live list or library of the workspace.
func borrowable(ctx context.Context, t *dms.Service, workspaceID, id string) error {
	r, err := t.DAL.Resources().Resource(ctx, id)
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.DeletedAt != nil || r.WorkspaceID != workspaceID || r.Kind != refs.KindList && r.Kind != refs.KindLibrary {
		return dms.ErrNotFound
	}
	return nil
}

// channel returns the channel of c with key.
func channel(c store.Connection, key string) (Channel, bool) {
	var channels []Channel
	if json.Unmarshal(c.Channels, &channels) != nil {
		return Channel{}, false
	}
	for _, ch := range channels {
		if ch.Key == key {
			return ch, true
		}
	}
	return Channel{}, false
}

func channelsOf(c store.Connection) []Channel {
	var channels []Channel
	_ = json.Unmarshal(c.Channels, &channels)
	return channels
}

func settingsOf(c store.Connection) Settings {
	var s Settings
	_ = json.Unmarshal(c.Settings, &s)
	return s
}

// open decrypts what a stored connection keeps sealed.
func (s *Service) open(c store.Connection) (sealed, error) {
	var out sealed
	plain, err := s.Secrets.Decrypt(integration.PurposeConnection, c.ID, c.ConfigCiphertext)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(plain), &out)
	return out, err
}

func (s *Service) seal(id string, v sealed) ([]byte, string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, "", err
	}
	ciphertext, err := s.Secrets.Encrypt(integration.PurposeConnection, id, string(raw))
	if err != nil {
		return nil, "", err
	}
	return ciphertext, s.Secrets.Hash(string(raw)), nil
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// prepare checks a save of kind and returns what the connection keeps:
// its plain settings, channels and sealed configuration. previous is what
// a replace keeps when the save leaves it out.
func prepare(ctx context.Context, t *dms.Service, subject, workspaceID string, kind Kind, in *Save, previous *sealed) (Settings, []Channel, sealed, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 200 {
		return Settings{}, nil, sealed{}, dms.Invalid("name needs 1 to 200 characters")
	}
	u, err := parseTarget(kind, in.Target)
	if err != nil {
		return Settings{}, nil, sealed{}, dms.Invalid("target: " + err.Error())
	}
	v := sealed{Target: u.String(), Headers: in.Headers}
	if in.TLS != nil {
		v.CA = strings.TrimSpace(in.TLS.CA)
	}
	switch {
	case in.Credentials != nil:
		v.Credentials = *in.Credentials
	case previous != nil:
		v.Credentials = previous.Credentials
	}
	if previous != nil {
		v.SigningSecret = previous.SigningSecret
	}
	if err = checkSealed(kind, &v); err != nil {
		return Settings{}, nil, sealed{}, dms.Invalid(err.Error())
	}
	settings := Settings{Timeout: in.Timeout, Sign: in.Sign, HealthPath: in.HealthPath}
	if err = checkSettings(kind, &settings); err != nil {
		return Settings{}, nil, sealed{}, dms.Invalid(err.Error())
	}
	if in.Channels == nil {
		in.Channels = []Channel{}
	}
	channels, err := normalizeChannels(kind, in.Channels)
	if err != nil {
		return Settings{}, nil, sealed{}, err
	}
	// A manager borrows access only from lists of the workspace they can
	// read, so a channel never names a place they cannot see.
	for _, ch := range channels {
		for _, use := range ch.Uses {
			on := ch.Access[use].On
			if on == OnWorkspace {
				continue
			}
			if err := borrowable(ctx, t, workspaceID, on); err != nil {
				if denied(err) == nil {
					return Settings{}, nil, sealed{}, dms.Invalid(fmt.Sprintf("channel %s: access is borrowed from the workspace or one of its lists or libraries", ch.Key))
				}
				return Settings{}, nil, sealed{}, err
			}
			if _, err := t.Authorize(ctx, subject, on, "read"); err != nil {
				if denied(err) == nil {
					return Settings{}, nil, sealed{}, dms.Invalid(fmt.Sprintf("channel %s: you cannot read the list its access is borrowed from", ch.Key))
				}
				return Settings{}, nil, sealed{}, err
			}
		}
	}
	return settings, channels, v, nil
}

func (s *Service) view(c store.Connection, v sealed) View {
	settings := settingsOf(c)
	out := View{ID: c.ID, WorkspaceID: c.WorkspaceID, Key: c.Key, Name: c.Name, Kind: c.Kind, Enabled: c.Enabled, Target: v.Target,
		TLS: TLS{CA: v.CA}, Credentials: v.Credentials.public(), Headers: v.Headers, Timeout: settings.Timeout, Sign: settings.Sign, HealthPath: settings.HealthPath,
		Channels: channelsOf(c), CreatedBy: c.CreatedBy, UpdatedBy: c.UpdatedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version}
	out.Status = s.runtime.Status(c)
	return out
}

func mapWrite(err error, what string) error {
	if errors.Is(err, dberr.ErrUniqueViolation) {
		return fmt.Errorf("%w: %s", dms.ErrConflict, what)
	}
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return dms.ErrPreconditionFailed
	}
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	return err
}

func audit(ctx context.Context, t *dms.Service, subject, action string, c store.Connection, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["connection_id"], details["key"], details["kind"] = c.ID, c.Key, string(c.Kind)
	return t.RecordEntity(ctx, subject, action, dms.AuditTarget{WorkspaceID: c.WorkspaceID, ResourceID: c.WorkspaceID, Kind: string(refs.KindWorkspace)}, details)
}

// List returns the connections of a workspace to its managers, by name.
func (s *Service) List(ctx context.Context, subject, workspaceID string) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		if err := requireManage(ctx, t, subject, workspaceID); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Connections().List(ctx, store.Filter{WorkspaceIDs: []string{workspaceID}})
		if err != nil {
			return nil, err
		}
		out := make([]View, 0, len(rows))
		for _, c := range rows {
			v, err := s.open(c)
			if err != nil {
				return nil, err
			}
			out = append(out, s.view(c, v))
		}
		return out, nil
	})
}

// Get returns a connection to its managers.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (View, error) {
		c, err := managed(ctx, t, subject, id)
		if err != nil {
			return View{}, err
		}
		v, err := s.open(c)
		if err != nil {
			return View{}, err
		}
		return s.view(c, v), nil
	})
}

// Create adds a connection to a workspace. An HTTP connection is given its
// signing secret, which the answer shows once.
func (s *Service) Create(ctx context.Context, d *dms.Service, subject string, in Save) (View, error) {
	if !in.Kind.Valid() {
		return View{}, dms.Invalid("kind is mqtt or http")
	}
	if in.Key == "" {
		in.Key = keyFromName(in.Name)
	}
	if !keyPattern.MatchString(in.Key) || len(in.Key) > 100 {
		return View{}, dms.Invalid("key is up to 100 lowercase letters, digits, - or _")
	}
	var out View
	err := d.Write(ctx, func(t *dms.Service) error {
		if err := requireManage(ctx, t, subject, in.WorkspaceID); err != nil {
			return err
		}
		settings, channels, v, err := prepare(ctx, t, subject, in.WorkspaceID, in.Kind, &in, nil)
		if err != nil {
			return err
		}
		if in.Kind == KindHTTP {
			v.SigningSecret = newSecret()
		}
		c := store.Connection{ID: uuid.NewString(), WorkspaceID: in.WorkspaceID, Key: in.Key, Name: in.Name, Kind: in.Kind, Enabled: in.Enabled == nil || *in.Enabled,
			Settings: marshal(settings), Channels: marshal(channels), CreatedBy: subject}
		if c.ConfigCiphertext, c.ConfigHash, err = s.seal(c.ID, v); err != nil {
			return err
		}
		if c, err = t.DAL.Connections().Create(ctx, c); err != nil {
			return mapWrite(err, "another connection of the workspace has this key or name")
		}
		out = s.view(c, v)
		out.SigningSecret = v.SigningSecret
		return audit(ctx, t, subject, "connection.create", c, nil)
	})
	if err == nil {
		s.runtime.Reload(out.ID)
	}
	return out, err
}

// Replace writes a connection read at version.
func (s *Service) Replace(ctx context.Context, d *dms.Service, subject, id string, version int, in Save) (View, error) {
	var out View
	err := d.Write(ctx, func(t *dms.Service) error {
		c, err := managed(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if in.Kind != "" && in.Kind != c.Kind || in.Key != "" && in.Key != c.Key || in.WorkspaceID != "" && in.WorkspaceID != c.WorkspaceID {
			return dms.Invalid("a connection's workspace, key and kind never change")
		}
		if c.Version != version {
			return dms.ErrPreconditionFailed
		}
		previous, err := s.open(c)
		if err != nil {
			return err
		}
		settings, channels, v, err := prepare(ctx, t, subject, c.WorkspaceID, c.Kind, &in, &previous)
		if err != nil {
			return err
		}
		next := store.Connection{Name: in.Name, Enabled: in.Enabled == nil || *in.Enabled, Settings: marshal(settings), Channels: marshal(channels)}
		if next.ConfigCiphertext, next.ConfigHash, err = s.seal(c.ID, v); err != nil {
			return err
		}
		if next, err = t.DAL.Connections().Replace(ctx, id, version, subject, next); err != nil {
			return mapWrite(err, "another connection of the workspace has this name")
		}
		out = s.view(next, v)
		return audit(ctx, t, subject, "connection.update", next, nil)
	})
	if err == nil {
		s.runtime.Reload(id)
	}
	return out, err
}

// Delete deletes a connection read at version.
func (s *Service) Delete(ctx context.Context, d *dms.Service, subject, id string, version int) error {
	err := d.Write(ctx, func(t *dms.Service) error {
		c, err := managed(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if c.Version != version {
			return dms.ErrPreconditionFailed
		}
		if err = t.DAL.Connections().Delete(ctx, id, version); err != nil {
			return mapWrite(err, "")
		}
		return audit(ctx, t, subject, "connection.delete", c, nil)
	})
	if err == nil {
		s.runtime.Reload(id)
	}
	return err
}

// RotateSigningSecret gives an HTTP connection a new signing secret, which
// the answer shows once.
func (s *Service) RotateSigningSecret(ctx context.Context, d *dms.Service, subject, id string) (View, error) {
	var out View
	err := d.Write(ctx, func(t *dms.Service) error {
		c, err := managed(ctx, t, subject, id)
		if err != nil {
			return err
		}
		if c.Kind != KindHTTP {
			return dms.Invalid("only HTTP connections sign their calls")
		}
		v, err := s.open(c)
		if err != nil {
			return err
		}
		v.SigningSecret = newSecret()
		next := c
		if next.ConfigCiphertext, next.ConfigHash, err = s.seal(c.ID, v); err != nil {
			return err
		}
		if next, err = t.DAL.Connections().Replace(ctx, id, c.Version, subject, next); err != nil {
			return mapWrite(err, "")
		}
		out = s.view(next, v)
		out.SigningSecret = v.SigningSecret
		return audit(ctx, t, subject, "connection.update", next, map[string]any{"signing_secret": "renewed"})
	})
	if err == nil {
		s.runtime.Reload(id)
	}
	return out, err
}

// Test connects or calls once with the connection's stored configuration
// and says what went wrong.
func (s *Service) Test(ctx context.Context, subject, id string) (TestResult, error) {
	c, err := dms.Read(ctx, s.DMS, func(t *dms.Service) (store.Connection, error) { return managed(ctx, t, subject, id) })
	if err != nil {
		return TestResult{}, err
	}
	v, err := s.open(c)
	if err != nil {
		return TestResult{}, err
	}
	return s.runtime.Test(ctx, c, v), nil
}

// usable returns the connection of the workspace with key, for a caller
// who may read the workspace.
func usable(ctx context.Context, t *dms.Service, subject, workspaceID, key string) (store.Connection, error) {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return store.Connection{}, err
	}
	rows, err := t.DAL.Connections().List(ctx, store.Filter{WorkspaceIDs: []string{workspaceID}, Key: key})
	if err != nil {
		return store.Connection{}, err
	}
	if len(rows) == 0 {
		return store.Connection{}, &httpapi.Problem{Status: 404, Code: "not_found", Detail: "no connection " + key + " in this workspace"}
	}
	return rows[0], nil
}

func (s *Service) useView(ctx context.Context, t *dms.Service, subject string, c store.Connection) (UseView, error) {
	out := UseView{Key: c.Key, Name: c.Name, Kind: c.Kind, Connected: c.Enabled && s.runtime.Status(c).Working(), Channels: []ChannelUseView{}}
	for _, ch := range channelsOf(c) {
		cv := ChannelUseView{Key: ch.Key, Name: ch.Name, Topics: ch.Topics, Path: ch.Path, Methods: ch.Methods, Retain: ch.Retain, Uses: ch.Uses, May: []Use{}}
		for _, use := range ch.Uses {
			err := May(ctx, t, subject, c, ch, use)
			var problem *httpapi.Problem
			if err != nil && !errors.As(err, &problem) {
				return UseView{}, err
			}
			if err == nil {
				cv.May = append(cv.May, use)
			}
		}
		out.Channels = append(out.Channels, cv)
	}
	return out, nil
}

// Uses returns a workspace's connections as its readers see them.
func (s *Service) Uses(ctx context.Context, subject, workspaceID string) ([]UseView, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]UseView, error) {
		if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Connections().List(ctx, store.Filter{WorkspaceIDs: []string{workspaceID}})
		if err != nil {
			return nil, err
		}
		out := make([]UseView, 0, len(rows))
		for _, c := range rows {
			v, err := s.useView(ctx, t, subject, c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	})
}

// Use returns one connection of a workspace as its readers see it.
func (s *Service) Use(ctx context.Context, subject, workspaceID, key string) (UseView, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (UseView, error) {
		c, err := usable(ctx, t, subject, workspaceID, key)
		if err != nil {
			return UseView{}, err
		}
		return s.useView(ctx, t, subject, c)
	})
}

// target is a connection and channel a caller acts on, with the use
// checked.
type target struct {
	conn    store.Connection
	channel Channel
}

// authorize returns the connection and channel subject may use this way,
// enabled or not.
func authorize(ctx context.Context, t *dms.Service, subject, workspaceID, key, channelKey string, use Use) (target, error) {
	c, err := usable(ctx, t, subject, workspaceID, key)
	if err != nil {
		return target{}, err
	}
	ch, ok := channel(c, channelKey)
	if !ok {
		return target{}, &httpapi.Problem{Status: 404, Code: "not_found", Detail: "connection " + key + " has no channel " + channelKey}
	}
	if err = May(ctx, t, subject, c, ch, use); err != nil {
		return target{}, err
	}
	return target{conn: c, channel: ch}, nil
}
