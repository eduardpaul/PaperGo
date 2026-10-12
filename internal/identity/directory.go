package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrGroupManagedByIdentityProvider = errors.New("group membership is managed by the identity provider")

type Directory struct{ DB *sql.DB }
type Principal struct {
	Type string `json:"principal_type"`
	ID   string `json:"principal_id"`
}
type Group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Version     int    `json:"version"`
}
type UserIdentity struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	Groups            []string
	SyncGroups        bool
}
type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Builtin     bool     `json:"builtin"`
	Version     int      `json:"version"`
}
type GroupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
}

func ValidateSubject(subject string) error {
	if subject == "" || subject != strings.TrimSpace(subject) || len(subject) > 255 || !utf8.ValidString(subject) {
		return invalid("principal ID must contain 1 to 255 bytes without surrounding whitespace")
	}
	return nil
}
func ValidatePrincipal(ctx context.Context, q Querier, p Principal) error {
	if err := ValidateSubject(p.ID); err != nil {
		return err
	}
	table := "identity_users"
	if p.Type == "group" {
		table = "identity_groups"
	} else if p.Type != "user" {
		return invalid("principal_type must be user or group")
	}
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE id=?)", p.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return invalid("principal must identify an existing " + p.Type)
	}
	return nil
}
func ValidateName(name, description string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 200 || !utf8.ValidString(name) || len(description) > 2000 || !utf8.ValidString(description) {
		return invalid("name must contain 1 to 200 bytes; description is limited to 2000 bytes")
	}
	return nil
}
func validateRole(in RoleInput) error {
	if err := ValidateName(in.Name, in.Description); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, scope := range Catalog {
		known[scope.Name] = true
	}
	seen := map[string]bool{}
	for _, scope := range in.Scopes {
		if !known[scope] || seen[scope] {
			return invalid("role scopes must be distinct entries from the scope catalog")
		}
		seen[scope] = true
	}
	return nil
}

func (d Directory) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (d Directory) write(ctx context.Context, actor, scope string, fn func(*sql.Tx) error) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireScope(ctx, tx, actor, scope); err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		return checkAdministrator(ctx, tx)
	})
}

// Initialize provisions built-in roles and the configured first administrator
// (by subject or by group) exactly once. A later configuration change never
// creates another administrator.
func (d Directory) Initialize(ctx context.Context, administrator, adminGroup string) error {
	if administrator != "" {
		if err := ValidateSubject(administrator); err != nil {
			return err
		}
	} else if adminGroup != "" {
		if err := ValidateName(adminGroup, ""); err != nil {
			return err
		}
	} else {
		return errors.New("administrator subject or admin group is required")
	}
	return d.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO identity_organizations(id,created_at,singleton,identifier,display_name,description,version) VALUES(?,CURRENT_TIMESTAMP,1,'papergo','PaperGo','',1) ON CONFLICT(singleton) DO NOTHING", uuid.NewString()); err != nil {
			return err
		}
		var initialized bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_roles WHERE id=?)", AdministratorRole).Scan(&initialized); err != nil {
			return err
		}
		if initialized {
			return nil
		}
		all, member := []string{}, []string{}
		for _, scope := range Catalog {
			all = append(all, scope.Name)
			if scope.GrantedToMembers {
				member = append(member, scope.Name)
			}
		}
		for _, r := range []Role{{ID: AdministratorRole, Name: "Administrator", Scopes: all}, {ID: MemberRole, Name: "Member", Scopes: member}} {
			raw, _ := json.Marshal(r.Scopes)
			if _, err := tx.ExecContext(ctx, "INSERT INTO identity_roles(id,name,description,scopes,builtin,version,created_at) VALUES(?,?, '',?,1,1,CURRENT_TIMESTAMP)", r.ID, r.Name, string(raw)); err != nil {
				return err
			}
		}
		if administrator != "" {
			if err := ensureUser(ctx, tx, administrator); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO identity_assignments(id,role_id,principal_type,principal_id,created_at) VALUES(?,?,'user',?,CURRENT_TIMESTAMP)", uuid.NewString(), AdministratorRole, administrator)
			return err
		}
		groupID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, "INSERT INTO identity_groups(id,name,description,source,version,created_at) VALUES(?,?,'','oidc',1,CURRENT_TIMESTAMP)", groupID, adminGroup); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO identity_assignments(id,role_id,principal_type,principal_id,created_at) VALUES(?,?,'group',?,CURRENT_TIMESTAMP)", uuid.NewString(), AdministratorRole, groupID)
		return err
	})
}

// EnsureUser enrolls a locally managed identity with the Member role on first use.
// Existing users keep their assignments, including an intentionally empty set.
func (d Directory) EnsureUser(ctx context.Context, subject string) error {
	if err := ValidateSubject(subject); err != nil {
		return err
	}
	var exists bool
	if err := d.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=?)", subject).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, subject, "user.create", "user", subject, func() error { return ensureUser(ctx, tx, subject) })
	})
}

// SyncUser enrolls or updates an OIDC user, synchronizing profile claims and
// memberships in oidc groups when groups were claimed in the token.
func (d Directory) SyncUser(ctx context.Context, u UserIdentity) error {
	if err := ValidateSubject(u.Subject); err != nil {
		return err
	}
	var validGroups []string
	if u.SyncGroups {
		seen := make(map[string]bool)
		for _, g := range u.Groups {
			if len(validGroups) >= 100 {
				slog.Warn("token groups exceed limit of 100, truncated", "subject", u.Subject)
				break
			}
			if err := ValidateName(g, ""); err != nil {
				continue
			}
			if !seen[g] {
				seen[g] = true
				validGroups = append(validGroups, g)
			}
		}
		slices.Sort(validGroups)
	}
	displayName := strings.TrimSpace(u.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(u.PreferredUsername)
	}
	if displayName == "" {
		displayName = u.Subject
	}
	if len([]rune(displayName)) > 200 {
		runes := []rune(displayName)
		displayName = string(runes[:200])
	}
	email := strings.TrimSpace(u.Email)
	if len(email) > 256 {
		email = ""
	}
	if email != "" {
		if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
			email = ""
		}
	}
	h := sha256.New()
	h.Write([]byte(displayName))
	h.Write([]byte{0})
	h.Write([]byte(email))
	h.Write([]byte{0})
	if u.SyncGroups {
		h.Write([]byte{1})
		for _, g := range validGroups {
			h.Write([]byte(g))
			h.Write([]byte{0})
		}
	} else {
		h.Write([]byte{0})
	}
	currentHash := hex.EncodeToString(h.Sum(nil))

	var active bool
	var storedHash string
	var deletedAt sql.NullTime
	err := d.DB.QueryRowContext(ctx, "SELECT active, sync_hash, deleted_at FROM identity_users WHERE id=?", u.Subject).Scan(&active, &storedHash, &deletedAt)
	if err == nil {
		if deletedAt.Valid {
			return nil
		}
		if storedHash == currentHash {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	return d.transaction(ctx, func(tx *sql.Tx) error {
		var userDeleted sql.NullTime
		err := tx.QueryRowContext(ctx, "SELECT deleted_at FROM identity_users WHERE id=?", u.Subject).Scan(&userDeleted)
		if err == nil && userDeleted.Valid {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		create := errors.Is(err, sql.ErrNoRows)
		if err = auditedIdentity(ctx, tx, u.Subject, "user.update", "user", u.Subject, func() error {
			if !create {
				_, err := tx.ExecContext(ctx, "UPDATE identity_users SET display_name=?,email=?,sync_hash=? WHERE id=? AND deleted_at IS NULL", displayName, email, currentHash, u.Subject)
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO identity_users(id,display_name,email,sync_hash,created_at) VALUES(?,?,?,?,CURRENT_TIMESTAMP)", u.Subject, displayName, email, currentHash); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO identity_assignments(id,role_id,principal_type,principal_id,created_at) VALUES(?,?,'user',?,CURRENT_TIMESTAMP)", uuid.NewString(), MemberRole, u.Subject)
			return err
		}); err != nil {
			return err
		}
		if !u.SyncGroups {
			return nil
		}

		desiredOIDCGroupIDs := make(map[string]bool)
		for _, g := range validGroups {
			var groupID, source string
			err := tx.QueryRowContext(ctx, "SELECT id, source FROM identity_groups WHERE name=?", g).Scan(&groupID, &source)
			if errors.Is(err, sql.ErrNoRows) {
				groupID = uuid.NewString()
				err = auditedIdentity(ctx, tx, u.Subject, "group.create", "group", groupID, func() error {
					_, err := tx.ExecContext(ctx, "INSERT INTO identity_groups(id,name,description,source,version,created_at) VALUES(?,?,?,'oidc',1,CURRENT_TIMESTAMP)", groupID, g, "")
					return err
				})
				if err != nil {
					return err
				}
				desiredOIDCGroupIDs[groupID] = true
			} else if err != nil {
				return err
			} else if source == "local" {
				slog.Warn("group claim matches local group, skipping", "group", g, "subject", u.Subject)
			} else {
				desiredOIDCGroupIDs[groupID] = true
			}
		}

		type membershipRow struct {
			id      string
			groupID string
		}
		rows, err := tx.QueryContext(ctx, "SELECT m.id, m.group_id FROM identity_memberships m JOIN identity_groups g ON g.id=m.group_id WHERE m.principal_type='user' AND m.principal_id=? AND g.source='oidc'", u.Subject)
		if err != nil {
			return err
		}
		defer rows.Close()
		var currentMemberships []membershipRow
		currentGroupIDs := make(map[string]bool)
		for rows.Next() {
			var m membershipRow
			if err = rows.Scan(&m.id, &m.groupID); err != nil {
				return err
			}
			currentMemberships = append(currentMemberships, m)
			currentGroupIDs[m.groupID] = true
		}
		if err = rows.Err(); err != nil {
			return err
		}

		hasActiveAdmin := func() (bool, error) {
			principal, _ := PrincipalSQL("a", "unused")
			principal = strings.ReplaceAll(principal, "?", "u.id")
			var exists bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_users u WHERE u.active=1 AND EXISTS(SELECT 1 FROM identity_assignments a WHERE a.role_id='administrator' AND `+principal+`))`).Scan(&exists)
			return exists, err
		}

		syncComplete := true
		for _, m := range currentMemberships {
			if !desiredOIDCGroupIDs[m.groupID] {
				if err = auditedIdentity(ctx, tx, u.Subject, "group.member", "group", m.groupID, func() error {
					if _, err := tx.ExecContext(ctx, "DELETE FROM identity_memberships WHERE id=?", m.id); err != nil {
						return err
					}
					hasAdmin, err := hasActiveAdmin()
					if err != nil {
						return err
					}
					if !hasAdmin {
						if _, err = tx.ExecContext(ctx, "INSERT INTO identity_memberships(id,group_id,principal_type,principal_id,created_at) VALUES(?,?, 'user',?,CURRENT_TIMESTAMP)", m.id, m.groupID, u.Subject); err != nil {
							return err
						}
						syncComplete = false
						slog.Warn("keeping administrator membership to prevent zero active administrators", "subject", u.Subject, "group_id", m.groupID)
					}
					return nil
				}); err != nil {
					return err
				}
			}
		}

		for groupID := range desiredOIDCGroupIDs {
			if !currentGroupIDs[groupID] {
				err = auditedIdentity(ctx, tx, u.Subject, "group.member", "group", groupID, func() error {
					_, err := tx.ExecContext(ctx, "INSERT INTO identity_memberships(id,group_id,principal_type,principal_id,created_at) VALUES(?,?, 'user',?,CURRENT_TIMESTAMP) ON CONFLICT(group_id,principal_type,principal_id) DO NOTHING", uuid.NewString(), groupID, u.Subject)
					return err
				})
				if err != nil {
					return err
				}
			}
		}
		if !syncComplete {
			// Retry deferred removals even if the next token has identical claims.
			_, err = tx.ExecContext(ctx, "UPDATE identity_users SET sync_hash='' WHERE id=?", u.Subject)
			return err
		}
		return nil
	})
}

func ensureUser(ctx context.Context, tx *sql.Tx, subject string) error {
	result, err := tx.ExecContext(ctx, "INSERT INTO identity_users(id,created_at) VALUES(?,CURRENT_TIMESTAMP) ON CONFLICT(id) DO NOTHING", subject)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO identity_assignments(id,role_id,principal_type,principal_id,created_at) VALUES(?,?,'user',?,CURRENT_TIMESTAMP)", uuid.NewString(), MemberRole, subject)
	return err
}
func (d Directory) CreateUser(ctx context.Context, actor string, in UserInput) (UserProfile, error) {
	var out UserProfile
	if err := ValidateSubject(in.Subject); err != nil {
		return out, err
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Email = strings.TrimSpace(in.Email)
	if err := validateProfile(in.DisplayName, in.Email); err != nil {
		return out, err
	}
	err := d.write(ctx, actor, "user.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "user.create", "user", in.Subject, func() error {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=?)", in.Subject).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return invalid("user already exists")
			}
			if err := ensureUser(ctx, tx, in.Subject); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE identity_users SET display_name=?,email=? WHERE id=?", in.DisplayName, in.Email, in.Subject); err != nil {
				return err
			}
			var err error
			out, err = userProfile(ctx, tx, in.Subject)
			return err
		})
	})
	return out, err
}
func (d Directory) Users(ctx context.Context, actor string) ([]UserProfile, error) {
	if err := RequireScope(ctx, d.DB, actor, "user.read"); err != nil {
		return nil, err
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT id,display_name,email,active,created_at FROM identity_users WHERE active=1 AND deleted_at IS NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserProfile{}
	for rows.Next() {
		var user UserProfile
		if err = rows.Scan(&user.Subject, &user.DisplayName, &user.Email, &user.Active, &user.CreatedAt); err != nil {
			return nil, err
		}
		if user.DisplayName == "" {
			user.DisplayName = user.Subject
		}
		out = append(out, user)
	}
	return out, rows.Err()
}
func (d Directory) Groups(ctx context.Context, actor string) ([]Group, error) {
	if err := RequireScope(ctx, d.DB, actor, "group.read"); err != nil {
		return nil, err
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT id,name,description,source,version FROM identity_groups ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err = rows.Scan(&g.ID, &g.Name, &g.Description, &g.Source, &g.Version); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (d Directory) SaveGroup(ctx context.Context, actor, id string, version int, in GroupInput) (Group, error) {
	if err := ValidateName(in.Name, in.Description); err != nil {
		return Group{}, err
	}
	create := id == ""
	if create {
		id = uuid.NewString()
		version = 0
	}
	var source string
	err := d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "group.update", "group", id, func() error {
			if create {
				source = "local"
				_, err := tx.ExecContext(ctx, "INSERT INTO identity_groups(id,name,description,source,version,created_at) VALUES(?,?,?,'local',1,CURRENT_TIMESTAMP)", id, in.Name, in.Description)
				return err
			}
			if err := tx.QueryRowContext(ctx, "SELECT source FROM identity_groups WHERE id=?", id).Scan(&source); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, "UPDATE identity_groups SET name=?,description=?,version=version+1 WHERE id=? AND version=?", in.Name, in.Description, id, version)
			return changed(result, err)
		})
	})
	return Group{ID: id, Name: in.Name, Description: in.Description, Source: source, Version: version + 1}, err
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrPreconditionFailed
	}
	return err
}
func (d Directory) DeleteGroup(ctx context.Context, actor, id string, version int) error {
	return d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "group.delete", "group", id, func() error {
			result, err := tx.ExecContext(ctx, "DELETE FROM identity_groups WHERE id=? AND version=?", id, version)
			return changed(result, err)
		})
	})
}
func (d Directory) Members(ctx context.Context, actor, id string) ([]Principal, error) {
	if err := RequireScope(ctx, d.DB, actor, "group.read"); err != nil {
		return nil, err
	}
	var exists bool
	if err := d.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_groups WHERE id=?)", id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT principal_type,principal_id FROM identity_memberships WHERE group_id=? ORDER BY principal_type,principal_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Principal{}
	for rows.Next() {
		var p Principal
		if err = rows.Scan(&p.Type, &p.ID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (d Directory) SetMember(ctx context.Context, actor, groupID string, p Principal, remove bool) error {
	return d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "group.member", "group", groupID, func() error {
			if err := ValidatePrincipal(ctx, tx, Principal{"group", groupID}); err != nil {
				return err
			}
			var source string
			if err := tx.QueryRowContext(ctx, "SELECT source FROM identity_groups WHERE id=?", groupID).Scan(&source); err != nil {
				return err
			}
			if source == "oidc" {
				return ErrGroupManagedByIdentityProvider
			}
			if err := ValidatePrincipal(ctx, tx, p); err != nil {
				return err
			}
			if remove {
				_, err := tx.ExecContext(ctx, "DELETE FROM identity_memberships WHERE group_id=? AND principal_type=? AND principal_id=?", groupID, p.Type, p.ID)
				return err
			}
			if p.Type == "group" {
				var cycle bool
				err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION SELECT principal_id FROM identity_memberships m JOIN descendants d ON m.group_id=d.id WHERE principal_type='group') SELECT EXISTS(SELECT 1 FROM descendants WHERE id=?)`, p.ID, groupID).Scan(&cycle)
				if err != nil {
					return err
				}
				if cycle {
					return invalid("nested groups must not form a cycle")
				}
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO identity_memberships(id,group_id,principal_type,principal_id,created_at) VALUES(?,?,?,?,CURRENT_TIMESTAMP) ON CONFLICT(group_id,principal_type,principal_id) DO NOTHING", uuid.NewString(), groupID, p.Type, p.ID)
			if err != nil {
				return err
			}
			var depth int
			err = tx.QueryRowContext(ctx, `WITH RECURSIVE paths(id,depth) AS (SELECT id,1 FROM identity_groups UNION SELECT m.principal_id,p.depth+1 FROM identity_memberships m JOIN paths p ON m.group_id=p.id WHERE m.principal_type='group' AND p.depth<=?) SELECT COALESCE(MAX(depth),0) FROM paths`, MaxGroupDepth).Scan(&depth)
			if err != nil {
				return err
			}
			if depth > MaxGroupDepth {
				return invalid("nested groups exceed the maximum depth of 10")
			}
			return nil
		})
	})
}
func (d Directory) Roles(ctx context.Context, actor string) ([]Role, error) {
	if err := RequireScope(ctx, d.DB, actor, "role.read"); err != nil {
		return nil, err
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT id,name,description,scopes,builtin,version FROM identity_roles ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		var raw string
		if err = rows.Scan(&r.ID, &r.Name, &r.Description, &raw, &r.Builtin, &r.Version); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Scopes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (d Directory) SaveRole(ctx context.Context, actor, id string, version int, in RoleInput) (Role, error) {
	if err := validateRole(in); err != nil {
		return Role{}, err
	}
	create := id == ""
	if create {
		id = uuid.NewString()
		version = 0
	}
	if in.Scopes == nil {
		in.Scopes = []string{}
	}
	raw, _ := json.Marshal(in.Scopes)
	err := d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "role.update", "role", id, func() error {
			if create {
				_, err := tx.ExecContext(ctx, "INSERT INTO identity_roles(id,name,description,scopes,builtin,version,created_at) VALUES(?,?,?,?,0,1,CURRENT_TIMESTAMP)", id, in.Name, in.Description, string(raw))
				return err
			}
			result, err := tx.ExecContext(ctx, "UPDATE identity_roles SET name=?,description=?,scopes=?,version=version+1 WHERE id=? AND version=? AND builtin=0", in.Name, in.Description, string(raw), id, version)
			return changed(result, err)
		})
	})
	return Role{ID: id, Name: in.Name, Description: in.Description, Scopes: in.Scopes, Version: version + 1}, err
}
func (d Directory) DeleteRole(ctx context.Context, actor, id string, version int) error {
	return d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "role.delete", "role", id, func() error {
			result, err := tx.ExecContext(ctx, "DELETE FROM identity_roles WHERE id=? AND version=? AND builtin=0", id, version)
			return changed(result, err)
		})
	})
}
func (d Directory) Assignments(ctx context.Context, actor, id string) ([]Principal, error) {
	if err := RequireScope(ctx, d.DB, actor, "role.read"); err != nil {
		return nil, err
	}
	var exists bool
	if err := d.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_roles WHERE id=?)", id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := d.DB.QueryContext(ctx, "SELECT principal_type,principal_id FROM identity_assignments WHERE role_id=? ORDER BY principal_type,principal_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Principal{}
	for rows.Next() {
		var p Principal
		if err = rows.Scan(&p.Type, &p.ID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (d Directory) SetAssignment(ctx context.Context, actor, roleID string, p Principal, remove bool) error {
	return d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, tx, actor, "role.assignment", "role", roleID, func() error {
			if err := ValidatePrincipal(ctx, tx, p); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_roles WHERE id=?)", roleID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
			if remove {
				_, err := tx.ExecContext(ctx, "DELETE FROM identity_assignments WHERE role_id=? AND principal_type=? AND principal_id=?", roleID, p.Type, p.ID)
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO identity_assignments(id,role_id,principal_type,principal_id,created_at) VALUES(?,?,?,?,CURRENT_TIMESTAMP) ON CONFLICT(role_id,principal_type,principal_id) DO NOTHING", uuid.NewString(), roleID, p.Type, p.ID)
			return err
		})
	})
}

// IsConstraint reports database constraint errors without depending on the DMS.
func IsConstraint(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&255 == 19
}

func checkAdministrator(ctx context.Context, tx *sql.Tx) error {
	// Check after every change, including nested memberships and role removal.
	principal, _ := PrincipalSQL("a", "unused")
	principal = strings.ReplaceAll(principal, "?", "u.id")
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_users u WHERE u.active=1 AND EXISTS(SELECT 1 FROM identity_assignments a WHERE a.role_id='administrator' AND `+principal+`))`).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrLastAdministrator
	}
	return nil
}
