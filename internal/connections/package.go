package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	store "papergo/internal/dal/connections"
	"papergo/internal/dal/dberr"
	"papergo/internal/dms"
	"papergo/internal/solutions"
)

// PackagePart is the name connections travel under in packages.
const PackagePart = "connections"

// portable is a connection as packages carry it: what it is and what it
// opens, never where it goes or how it signs in. Its target, CA,
// credentials, fixed headers and signing secret stay with this
// installation.
type portable struct {
	WorkspaceID string    `json:"workspace_id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Kind        Kind      `json:"kind"`
	Settings    Settings  `json:"settings"`
	Channels    []Channel `json:"channels"`
}

// part is connections' part of workspace and organization packages.
func (s *Service) part() dms.PackagePart {
	return dms.PackagePart{
		Name: PackagePart,
		Export: func(ctx context.Context, d *dms.Service, workspaceID string) (json.RawMessage, error) {
			f := store.Filter{}
			if workspaceID != "" {
				f.WorkspaceIDs = []string{workspaceID}
			}
			rows, err := d.DAL.Connections().List(ctx, f)
			if err != nil || len(rows) == 0 {
				return nil, err
			}
			out := make([]portable, len(rows))
			for i, c := range rows {
				out[i] = portable{WorkspaceID: c.WorkspaceID, Key: c.Key, Name: c.Name, Kind: c.Kind, Settings: settingsOf(c), Channels: channelsOf(c)}
			}
			return marshal(out), nil
		},
		Remap: func(raw json.RawMessage, id func(string) (string, bool)) (json.RawMessage, error) {
			var list []portable
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, err
			}
			for i := range list {
				ws, ok := id(list[i].WorkspaceID)
				if !ok {
					return nil, fmt.Errorf("connection %s belongs to a workspace the package does not carry", list[i].Key)
				}
				list[i].WorkspaceID = ws
				for _, ch := range list[i].Channels {
					for _, a := range ch.Access {
						if a == nil || a.On == OnWorkspace {
							continue
						}
						on, ok := id(a.On)
						if !ok {
							return nil, fmt.Errorf("channel %s of connection %s borrows its access from a list the package does not carry", ch.Key, list[i].Key)
						}
						a.On = on
					}
				}
			}
			return marshal(list), nil
		},
		Import: func(ctx context.Context, d *dms.Service, subject string, raw json.RawMessage) ([]string, error) {
			var list []portable
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, fmt.Errorf("connections: %w", err)
			}
			var warnings []string
			for _, p := range list {
				// An organization package carries the recycle bin too, and a
				// connection belongs to a live workspace.
				ws, err := d.DAL.Resources().Resource(ctx, p.WorkspaceID)
				if err != nil && !dberr.IsNotFound(err) {
					return nil, err
				}
				if err != nil || ws.DeletedAt != nil {
					warnings = append(warnings, fmt.Sprintf("Connection %s (%s) was left out: its workspace is in the recycle bin.", p.Name, p.Key))
					continue
				}
				c, err := s.adopt(ctx, d, subject, p)
				if err != nil {
					return nil, fmt.Errorf("connection %s: %w", p.Key, err)
				}
				warnings = append(warnings, fmt.Sprintf("Give connection %s (%s) its address and credentials in Devices & services, then enable it: packages leave them behind.", c.Name, c.Key))
			}
			return warnings, nil
		},
	}
}

// adopt records a connection a package or a solution carries, disabled
// and without a target, until a manager gives it one.
func (s *Service) adopt(ctx context.Context, d *dms.Service, subject string, p portable) (store.Connection, error) {
	if !p.Kind.Valid() || !keyPattern.MatchString(p.Key) {
		return store.Connection{}, dms.Invalid("a connection names its key and kind, mqtt or http")
	}
	channels, err := normalizeChannels(p.Kind, p.Channels)
	if err != nil {
		return store.Connection{}, err
	}
	settings := p.Settings
	if err = checkSettings(p.Kind, &settings); err != nil {
		return store.Connection{}, dms.Invalid(err.Error())
	}
	c := store.Connection{ID: uuid.NewString(), WorkspaceID: p.WorkspaceID, Key: p.Key, Name: p.Name, Kind: p.Kind, Enabled: false,
		Settings: marshal(settings), Channels: marshal(channels), CreatedBy: subject}
	v := sealed{Credentials: Credentials{Type: CredentialNone}}
	if p.Kind == KindHTTP {
		v.SigningSecret = newSecret()
	}
	if c.ConfigCiphertext, c.ConfigHash, err = s.seal(c.ID, v); err != nil {
		return store.Connection{}, err
	}
	if c, err = d.DAL.Connections().Create(ctx, c); err != nil {
		return store.Connection{}, mapWrite(err, "another connection of the workspace has this key or name")
	}
	return c, audit(ctx, d, subject, "connection.create", c, map[string]any{"adopted": true})
}

// declared is a connection as a solution declares it.
type declared struct {
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Kind     Kind      `json:"kind"`
	Settings Settings  `json:"settings,omitzero"`
	Channels []Channel `json:"channels"`
}

// wantOf is what a solution wants a connection to be, as Find, Create and
// Update report it, so the two compare.
func wantOf(name string, kind Kind, settings Settings, channels []Channel) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(marshal(map[string]any{"name": name, "kind": kind, "settings": settings, "channels": channels}), &out)
	return out
}

func stateOf(c store.Connection) map[string]any {
	return wantOf(c.Name, c.Kind, settingsOf(c), channelsOf(c))
}

// solutionPart is connections' part of solutions: a solution declares the
// connections its apps and workflows name, with channels whose access may
// borrow from its collections ($collection:<key>). Installing creates them
// disabled, without an address, until a manager gives them one; a later
// version changes their name, settings and channels, and never their
// address or credentials.
func (s *Service) solutionPart() solutions.Part {
	return solutions.Part{
		Name: PackagePart,
		Objects: func(raw json.RawMessage) ([]solutions.PartObject, error) {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			var list []declared
			if err := dec.Decode(&list); err != nil {
				return nil, fmt.Errorf("connections: %v", err)
			}
			out := make([]solutions.PartObject, 0, len(list))
			seen := map[string]bool{}
			for i, d := range list {
				at := fmt.Sprintf("[%d]: ", i)
				if !keyPattern.MatchString(d.Key) || len(d.Key) > 100 || seen[d.Key] {
					return nil, fmt.Errorf("%skey is a distinct key of lowercase letters, digits, - and _", at)
				}
				seen[d.Key] = true
				if !d.Kind.Valid() {
					return nil, fmt.Errorf("%skind is mqtt or http", at)
				}
				if d.Name == "" {
					d.Name = d.Key
				}
				channels, err := normalizeChannels(d.Kind, d.Channels)
				if err != nil {
					return nil, fmt.Errorf("%s%v", at, err)
				}
				settings := d.Settings
				if err = checkSettings(d.Kind, &settings); err != nil {
					return nil, fmt.Errorf("%s%v", at, err)
				}
				out = append(out, solutions.PartObject{Key: d.Key, Name: d.Name, Want: wantOf(d.Name, d.Kind, settings, channels)})
			}
			return out, nil
		},
		Find: func(ctx context.Context, t *dms.Service, subject, workspaceID, key string) (string, map[string]any, bool, error) {
			if err := requireManage(ctx, t, subject, workspaceID); err != nil {
				return "", nil, false, err
			}
			rows, err := t.DAL.Connections().List(ctx, store.Filter{WorkspaceIDs: []string{workspaceID}, Key: key})
			if err != nil || len(rows) == 0 {
				return "", nil, false, err
			}
			return rows[0].ID, stateOf(rows[0]), true, nil
		},
		Create: func(ctx context.Context, t *dms.Service, subject, workspaceID string, o solutions.PartObject) (string, map[string]any, string, error) {
			if err := requireManage(ctx, t, subject, workspaceID); err != nil {
				return "", nil, "", err
			}
			p, err := portableOf(workspaceID, o)
			if err != nil {
				return "", nil, "", err
			}
			c, err := s.adopt(ctx, t, subject, p)
			if err != nil {
				return "", nil, "", err
			}
			s.runtime.Reload(c.ID)
			return c.ID, stateOf(c), fmt.Sprintf("Give connection %s its address and credentials in Devices & services, then enable it.", c.Key), nil
		},
		Update: func(ctx context.Context, t *dms.Service, subject, id string, o solutions.PartObject) (map[string]any, error) {
			c, err := managed(ctx, t, subject, id)
			if err != nil {
				return nil, err
			}
			p, err := portableOf(c.WorkspaceID, o)
			if err != nil {
				return nil, err
			}
			if p.Kind != c.Kind {
				return nil, dms.Invalid("connection " + c.Key + " is " + string(c.Kind) + "; a connection's kind never changes")
			}
			next := c
			next.Name, next.Settings, next.Channels = p.Name, marshal(p.Settings), marshal(p.Channels)
			if next, err = t.DAL.Connections().Replace(ctx, id, c.Version, subject, next); err != nil {
				return nil, mapWrite(err, "another connection of the workspace has this name")
			}
			if err = audit(ctx, t, subject, "connection.update", next, map[string]any{"solution": true}); err != nil {
				return nil, err
			}
			s.runtime.Reload(id)
			return stateOf(next), nil
		},
	}
}

// portableOf reads back what Objects made of a declared connection.
func portableOf(workspaceID string, o solutions.PartObject) (portable, error) {
	var p portable
	if err := json.Unmarshal(marshal(o.Want), &p); err != nil {
		return p, err
	}
	p.WorkspaceID, p.Key = workspaceID, o.Key
	return p, nil
}
