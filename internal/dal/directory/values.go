package directory

type PrincipalType string

const (
	PrincipalUser  PrincipalType = "user"
	PrincipalGroup PrincipalType = "group"
)

func (v PrincipalType) Valid() bool { return v == PrincipalUser || v == PrincipalGroup }

type GroupSource string

const (
	SourceLocal GroupSource = "local"
	SourceOIDC  GroupSource = "oidc"
)

func (v GroupSource) Valid() bool { return v == SourceLocal || v == SourceOIDC }

type PreferenceOwner string

const (
	OwnerUser         PreferenceOwner = "user"
	OwnerOrganization PreferenceOwner = "organization"
)

func (v PreferenceOwner) Valid() bool { return v == OwnerUser || v == OwnerOrganization }

// EntityKind identifies a public directory entity in an audit snapshot.
type EntityKind string

const (
	EntityUser         EntityKind = "user"
	EntityGroup        EntityKind = "group"
	EntityRole         EntityKind = "role"
	EntityOrganization EntityKind = "organization"
	EntityAccessToken  EntityKind = "access_token"
)

func (v EntityKind) Valid() bool {
	switch v {
	case EntityUser, EntityGroup, EntityRole, EntityOrganization, EntityAccessToken:
		return true
	}
	return false
}

// Change describes the directory operation recorded by an audit event.
type Change string

const (
	ChangeCreate     Change = "create"
	ChangeUpdate     Change = "update"
	ChangeDelete     Change = "delete"
	ChangeStatus     Change = "status"
	ChangeMember     Change = "member"
	ChangeAssignment Change = "assignment"
	ChangeRevoke     Change = "revoke"
)

func (v Change) Valid() bool {
	switch v {
	case ChangeCreate, ChangeUpdate, ChangeDelete, ChangeStatus, ChangeMember, ChangeAssignment, ChangeRevoke:
		return true
	}
	return false
}
func (v EntityKind) AuditType() string                { return "identity." + string(v) }
func (v EntityKind) AuditAction(change Change) string { return string(v) + "." + string(change) }
