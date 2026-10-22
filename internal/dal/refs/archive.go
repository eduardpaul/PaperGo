package refs

// ArchiveSelection controls discovery, independently of publication and deletion.
// The zero value is unfiltered inside DAL; public APIs normalize it to active.
type ArchiveSelection string

const (
	ArchiveActive   ArchiveSelection = "active"
	ArchiveArchived ArchiveSelection = "archived"
	ArchiveAll      ArchiveSelection = "all"
)

func (a ArchiveSelection) Valid() bool {
	return a == "" || a == ArchiveActive || a == ArchiveArchived || a == ArchiveAll
}
func (a ArchiveSelection) Default() ArchiveSelection {
	if a == "" {
		return ArchiveActive
	}
	return a
}
func (a ArchiveSelection) Matches(archived bool) bool {
	return a == "" || a == ArchiveAll || (a == ArchiveArchived) == archived
}

// Predicate uses a DAL-owned SQL alias and no user input.
func (a ArchiveSelection) Predicate(alias string) string {
	switch a {
	case ArchiveActive:
		return alias + ".archived_at IS NULL"
	case ArchiveArchived:
		return alias + ".archived_at IS NOT NULL"
	default:
		return "TRUE"
	}
}
