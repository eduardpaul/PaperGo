// Package recipients resolves the recipient entries that tasks and
// notifications share, such as role:owners or field:reviewer, into people
// and groups, and groups into their people.
package recipients

import (
	"context"
	"slices"
	"strings"

	"papergo/internal/dal/directory"
	grantstore "papergo/internal/dal/grants"
	"papergo/internal/dms"
)

// Resolve turns recipient entries into subjects and group:<id> entries,
// sorted and without duplicates.
//
//	<subject>                  a user
//	group:<id>                 a group, nested groups included
//	role:owners, role:members  the workspace's owners or members
//	creator                    the item's creator
//	actor                      who caused the run
//	field:<key>                the people in a field of the item
//
// No entries means the workspace's owners. Unknown users and groups fail
// with their names; inactive users are left out. subject reads the item.
func Resolve(ctx context.Context, t *dms.Service, subject string, entries []string, workspaceID, itemID, actor string) ([]string, error) {
	var out []string
	var unknown []string
	var itemLoaded bool
	var creator string
	var itemValues map[string]any
	loadItem := func(entry string) error {
		if itemID == "" {
			return dms.Invalid(entry + " needs an item")
		}
		if !itemLoaded {
			r, err := t.GetSurface(ctx, subject, itemID, "auto")
			if err != nil {
				return err
			}
			creator, itemValues, itemLoaded = r.CreatedBy, r.Values, true
		}
		return nil
	}
	if len(entries) == 0 {
		entries = []string{"role:owners"}
	}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		switch {
		case entry == "":
		case entry == "role:owners":
			owners, err := t.WorkspaceManagers(ctx, workspaceID)
			if err != nil {
				return nil, err
			}
			out = append(out, owners...)
		case entry == "role:members":
			members, err := t.DAL.Grants().List(ctx, grantstore.Filter{ResourceID: workspaceID, PrincipalType: grantstore.PrincipalUser})
			if err != nil {
				return nil, err
			}
			for _, g := range members {
				out = append(out, g.PrincipalID)
			}
		case entry == "creator":
			if err := loadItem(entry); err != nil {
				return nil, err
			}
			out = append(out, creator)
		case entry == "actor":
			if actor == "" {
				return nil, dms.Invalid("actor needs a run caused by a person")
			}
			out = append(out, actor)
		case strings.HasPrefix(entry, "field:"):
			if err := loadItem(entry); err != nil {
				return nil, err
			}
			switch v := itemValues[strings.TrimPrefix(entry, "field:")].(type) {
			case string:
				out = append(out, v)
			case []any:
				for _, x := range v {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
			}
		case strings.HasPrefix(entry, "group:"):
			ok, err := t.DAL.Directory().GroupExists(ctx, strings.TrimPrefix(entry, "group:"))
			if err != nil {
				return nil, err
			}
			if !ok {
				unknown = append(unknown, entry)
				continue
			}
			out = append(out, entry)
		case strings.HasPrefix(entry, "role:"):
			return nil, dms.Invalid("role is owners or members, as in role:owners")
		default:
			out = append(out, entry)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	users := make([]string, 0, len(out))
	for _, a := range out {
		if !strings.HasPrefix(a, "group:") {
			users = append(users, a)
		}
	}
	if len(users) > 0 {
		rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: users})
		if err != nil {
			return nil, err
		}
		known := map[string]bool{}
		inactive := map[string]bool{}
		for _, u := range rows {
			known[u.ID] = true
			inactive[u.ID] = !u.Active
		}
		out = slices.DeleteFunc(out, func(a string) bool {
			if strings.HasPrefix(a, "group:") {
				return false
			}
			if !known[a] {
				unknown = append(unknown, a)
				return true
			}
			return inactive[a]
		})
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, dms.Invalid("unknown recipients: " + strings.Join(slices.Compact(unknown), ", "))
	}
	if len(out) == 0 {
		return nil, dms.Invalid("no one is left: the recipients give no active user or group, or the workspace has no owners")
	}
	return out, nil
}

// People expands resolved entries into the active people they name: users
// stay, and group:<id> entries become their members, nested groups
// included. The result is sorted and without duplicates.
func People(ctx context.Context, t *dms.Service, entries []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	groups := []string{}
	for _, e := range entries {
		if id, ok := strings.CutPrefix(e, "group:"); ok {
			groups = append(groups, id)
		} else {
			out = append(out, e)
		}
	}
	for len(groups) > 0 {
		id := groups[0]
		groups = groups[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		members, err := t.DAL.Directory().Members(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.Type == directory.PrincipalGroup {
				groups = append(groups, m.ID)
			} else {
				out = append(out, m.ID)
			}
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return out, nil
	}
	rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: out})
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, u := range rows {
		active[u.ID] = u.Active && u.DeletedAt == nil
	}
	return slices.DeleteFunc(out, func(s string) bool { return !active[s] }), nil
}
