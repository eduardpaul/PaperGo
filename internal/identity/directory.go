package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
)

var ErrGroupManagedByIdentityProvider = directory.ErrManagedGroup

type Directory struct{ DB *sql.DB }

func (d Directory) store(tx *sql.Tx) *dal.Store { return dal.NewStore(d.DB).WithTx(tx) }

type Principal = directory.Principal
type Group struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Source      directory.GroupSource `json:"source"`
	Version     int                   `json:"version"`
}

func groupRecord(g directory.Group) Group {
	return Group{g.ID, g.Name, g.Description, g.Source, g.Version}
}

type UserIdentity struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	Groups            []string
	SyncGroups        bool
}
type Role = directory.Role
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
func ValidatePrincipal(ctx context.Context, store *dal.Store, p Principal) error {
	if err := ValidateSubject(p.ID); err != nil {
		return err
	}
	if !p.Type.Valid() {
		return invalid("principal_type must be user or group")
	}
	err := store.Directory().ValidatePrincipal(ctx, p)
	if errors.Is(err, dberr.ErrInvalidReference) {
		return invalid("principal must identify an existing " + string(p.Type))
	}
	return err
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

// transaction runs fn in a serializable transaction, again when the database
// refuses it for a concurrent one.
func (d Directory) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	return dal.Retry(ctx, func() error {
		tx, err := d.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := d.store(tx).Directory().Lock(ctx); err != nil {
			return err
		}
		if err = fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (d Directory) write(ctx context.Context, actor, scope string, fn func(*sql.Tx) error) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireScope(ctx, d.store(tx), actor, scope); err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		return checkAdministrator(ctx, d.store(tx))
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
		repo := d.store(tx).Directory()
		if err := repo.EnsureOrganization(ctx); err != nil {
			return err
		}
		initialized, err := repo.RoleExists(ctx, AdministratorRole)
		if err != nil {
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
		for _, r := range []Role{{ID: AdministratorRole, Name: "Administrator", Scopes: all, Builtin: true}, {ID: MemberRole, Name: "Member", Scopes: member, Builtin: true}} {
			if _, err := repo.CreateRole(ctx, r); err != nil {
				return err
			}
		}
		if administrator != "" {
			if err := ensureUser(ctx, d.store(tx), administrator); err != nil {
				return err
			}
			return repo.SetAssignment(ctx, AdministratorRole, Principal{directory.PrincipalUser, administrator}, false)
		}
		g, err := repo.CreateGroup(ctx, "", adminGroup, "", directory.SourceOIDC)
		if err != nil {
			return err
		}
		return repo.SetAssignment(ctx, AdministratorRole, Principal{directory.PrincipalGroup, g.ID}, false)
	})
}

// EnsureUser enrolls a locally managed identity with the Member role on first use.
// Existing users keep their assignments, including an intentionally empty set.
func (d Directory) EnsureUser(ctx context.Context, subject string) error {
	if err := ValidateSubject(subject); err != nil {
		return err
	}
	user, err := dal.NewStore(d.DB).Directory().User(ctx, subject)
	if err == nil {
		if user.Service {
			return ErrForbidden
		}
		return nil
	}
	if !dberr.IsNotFound(err) {
		return err
	}

	return d.transaction(ctx, func(tx *sql.Tx) error {
		u, err := d.store(tx).Directory().User(ctx, subject)
		if err == nil {
			if u.Service {
				return ErrForbidden
			}
			return nil
		}
		if !dberr.IsNotFound(err) {
			return err
		}
		return auditedIdentity(ctx, d.store(tx), subject, directory.EntityUser, directory.ChangeCreate, subject, func() error { return ensureUser(ctx, d.store(tx), subject) })
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

	existing, err := dal.NewStore(d.DB).Directory().User(ctx, u.Subject)
	if err == nil {
		if existing.Service {
			return ErrForbidden
		}
		if existing.DeletedAt != nil || existing.SyncHash == currentHash {
			return nil
		}
	} else if !dberr.IsNotFound(err) {
		return err
	}

	return d.transaction(ctx, func(tx *sql.Tx) error {
		repo := d.store(tx).Directory()
		user, err := repo.User(ctx, u.Subject)
		create := dberr.IsNotFound(err)
		if err != nil && !create {
			return err
		}
		if !create {
			if user.Service {
				return ErrForbidden
			}
			if user.DeletedAt != nil || user.SyncHash == currentHash {
				return nil
			}
		}
		if err := auditedIdentity(ctx, d.store(tx), u.Subject, directory.EntityUser, directory.ChangeUpdate, u.Subject, func() error {
			if create {
				if err := ensureUser(ctx, d.store(tx), u.Subject); err != nil {
					return err
				}
				var e error
				user, e = repo.User(ctx, u.Subject)
				if e != nil {
					return e
				}
			}
			user.DisplayName = displayName
			user.Email = email
			user.SyncHash = currentHash
			return repo.SaveUser(ctx, user)
		}); err != nil {
			return err
		}
		if !u.SyncGroups {
			return nil
		}
		desired := map[string]bool{}
		for _, name := range validGroups {
			g, err := repo.GroupByName(ctx, name)
			if dberr.IsNotFound(err) {
				id := uuid.NewString()
				err = auditedIdentity(ctx, d.store(tx), u.Subject, directory.EntityGroup, directory.ChangeCreate, id, func() error { var e error; g, e = repo.CreateGroup(ctx, id, name, "", directory.SourceOIDC); return e })
			}
			if err != nil {
				return err
			}
			if g.Source == directory.SourceLocal {
				slog.Warn("group claim matches local group, skipping", "group", name, "subject", u.Subject)
				continue
			}
			desired[g.ID] = true
		}
		memberships, err := repo.Memberships(ctx, directory.MembershipFilter{Principal: &Principal{Type: directory.PrincipalUser, ID: u.Subject}})
		if err != nil {
			return err
		}
		current := map[string]bool{}
		complete := true
		for _, m := range memberships {
			if m.Principal.Type != directory.PrincipalUser || m.Principal.ID != u.Subject {
				continue
			}
			g, err := repo.Group(ctx, m.GroupID)
			if err != nil {
				return err
			}
			if g.Source != directory.SourceOIDC {
				continue
			}
			current[g.ID] = true
			if desired[g.ID] {
				continue
			}
			if err := auditedIdentity(ctx, d.store(tx), u.Subject, directory.EntityGroup, directory.ChangeMember, g.ID, func() error {
				if err := repo.SyncMembership(ctx, g.ID, u.Subject, true); err != nil {
					return err
				}
				has, err := repo.HasAdministrator(ctx, AdministratorRole)
				if err != nil {
					return err
				}
				if !has {
					complete = false
					slog.Warn("keeping administrator membership to prevent zero active administrators", "subject", u.Subject, "group_id", g.ID)
					return repo.SyncMembership(ctx, g.ID, u.Subject, false)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		for id := range desired {
			if !current[id] {
				if err := auditedIdentity(ctx, d.store(tx), u.Subject, directory.EntityGroup, directory.ChangeMember, id, func() error { return repo.SyncMembership(ctx, id, u.Subject, false) }); err != nil {
					return err
				}
			}
		}
		if !complete {
			user.SyncHash = ""
			return repo.SaveUser(ctx, user)
		}
		return nil
	})
}
func ensureUser(ctx context.Context, store *dal.Store, subject string) error {
	repo := store.Directory()
	_, err := repo.User(ctx, subject)
	if err == nil {
		return nil
	}
	if !dberr.IsNotFound(err) {
		return err
	}
	if _, err := repo.CreateUser(ctx, subject, "", "", true, false); err != nil {
		return err
	}
	return repo.SetAssignment(ctx, MemberRole, Principal{directory.PrincipalUser, subject}, false)
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
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityUser, directory.ChangeCreate, in.Subject, func() error {
			_, err := d.store(tx).Directory().User(ctx, in.Subject)
			if err == nil {
				return invalid("user already exists")
			}
			if !dberr.IsNotFound(err) {
				return err
			}
			if err := ensureUser(ctx, d.store(tx), in.Subject); err != nil {
				return err
			}
			user, err := d.store(tx).Directory().User(ctx, in.Subject)
			if err != nil {
				return err
			}
			user.DisplayName = in.DisplayName
			user.Email = in.Email
			if err := d.store(tx).Directory().SaveUser(ctx, user); err != nil {
				return err
			}
			out, err = userProfile(ctx, d.store(tx), in.Subject)
			return err
		})
	})
	return out, err
}
func (d Directory) Users(ctx context.Context, actor string) ([]UserProfile, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "user.read"); err != nil {
		return nil, err
	}
	users, err := dal.NewStore(d.DB).Directory().Users(ctx, directory.UserFilter{Active: true})
	if err != nil {
		return nil, err
	}
	out := []UserProfile{}
	for _, u := range users {
		if u.DeletedAt == nil {
			out = append(out, profileRecord(u))
		}
	}
	return out, nil
}
func (d Directory) Groups(ctx context.Context, actor string) ([]Group, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "group.read"); err != nil {
		return nil, err
	}
	gs, err := dal.NewStore(d.DB).Directory().Groups(ctx)
	out := []Group{}
	for _, g := range gs {
		out = append(out, groupRecord(g))
	}
	return out, err
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
	var out directory.Group
	err := d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityGroup, directory.ChangeUpdate, id, func() error {
			repo := d.store(tx).Directory()
			if create {
				var err error
				out, err = repo.CreateGroup(ctx, id, in.Name, in.Description, directory.SourceLocal)
				return err
			}
			g, err := repo.Group(ctx, id)
			if err != nil {
				return err
			}
			g.Name = in.Name
			g.Description = in.Description
			g.Version = version + 1
			out = g
			return repo.SaveGroup(ctx, g, version)
		})
	})
	return groupRecord(out), identityError(err)
}
func (d Directory) DeleteGroup(ctx context.Context, actor, id string, version int) error {
	return identityError(d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityGroup, directory.ChangeDelete, id, func() error { return d.store(tx).Directory().DeleteGroup(ctx, id, version) })
	}))
}
func (d Directory) Members(ctx context.Context, actor, id string) ([]Principal, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "group.read"); err != nil {
		return nil, err
	}
	out, err := dal.NewStore(d.DB).Directory().Members(ctx, id)
	return out, identityError(err)
}
func (d Directory) SetMember(ctx context.Context, actor, groupID string, p Principal, remove bool) error {
	return identityError(d.write(ctx, actor, "group.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityGroup, directory.ChangeMember, groupID, func() error {
			return d.store(tx).Directory().SetMember(ctx, groupID, p, remove)
		})
	}))
}
func (d Directory) Roles(ctx context.Context, actor string) ([]Role, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "role.read"); err != nil {
		return nil, err
	}
	return dal.NewStore(d.DB).Directory().Roles(ctx)
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
	role := Role{ID: id, Name: in.Name, Description: in.Description, Scopes: in.Scopes, Version: version + 1}
	err := d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityRole, directory.ChangeUpdate, id, func() error {
			repo := d.store(tx).Directory()
			if create {
				_, err := repo.CreateRole(ctx, role)
				return err
			}
			return repo.SaveRole(ctx, role, version)
		})
	})
	return role, identityError(err)
}
func (d Directory) DeleteRole(ctx context.Context, actor, id string, version int) error {
	return identityError(d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityRole, directory.ChangeDelete, id, func() error { return d.store(tx).Directory().DeleteRole(ctx, id, version) })
	}))
}
func (d Directory) Assignments(ctx context.Context, actor, id string) ([]Principal, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "role.read"); err != nil {
		return nil, err
	}
	out, err := dal.NewStore(d.DB).Directory().Assignments(ctx, id)
	return out, identityError(err)
}
func (d Directory) SetAssignment(ctx context.Context, actor, roleID string, p Principal, remove bool) error {
	return identityError(d.write(ctx, actor, "role.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityRole, directory.ChangeAssignment, roleID, func() error {
			if err := ValidatePrincipal(ctx, d.store(tx), p); err != nil {
				return err
			}
			exists, err := d.store(tx).Directory().RoleExists(ctx, roleID)
			if err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
			return d.store(tx).Directory().SetAssignment(ctx, roleID, p, remove)
		})
	}))
}

func identityError(err error) error {
	switch {
	case dberr.IsNotFound(err):
		return ErrNotFound
	case errors.Is(err, dberr.ErrConcurrentUpdate):
		return ErrPreconditionFailed
	case errors.Is(err, dberr.ErrCycleDetected):
		return invalid("nested groups must not form a cycle")
	case errors.Is(err, dberr.ErrInvalidReference):
		return invalid(err.Error())
	case errors.Is(err, dberr.ErrCheckViolation):
		return invalid(err.Error())
	}
	return err
}
func checkAdministrator(ctx context.Context, store *dal.Store) error {
	exists, err := store.Directory().HasAdministrator(ctx, AdministratorRole)
	if err != nil {
		return err
	}
	if !exists {
		return ErrLastAdministrator
	}
	return nil
}
