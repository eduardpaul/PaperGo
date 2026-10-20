package content

import (
	"context"
	"fmt"
	"strings"
	"time"

	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// RelationshipExploreQuery always retains WorkspaceID, even when ItemID is external.
// After is a stable ID cursor. Discovery queries have their own name/ID cursor.
type RelationshipExploreQuery struct {
	WorkspaceID, ItemID, CollectionID, TypeID, Scope, Direction, Search, After string
	Filter                                                                     *LinkFilter
	Untyped                                                                    bool
	Directed                                                                   *bool
	Limit                                                                      int
	Reader                                                                     access.Principal
}

// RelationshipEndpoint contains only visible-surface navigation information.
type RelationshipEndpoint struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceName  string `json:"workspace_name"`
	CollectionID   string `json:"collection_id"`
	CollectionName string `json:"collection_name"`
	// SortKey is the folded name discovery pages are ordered and resumed by.
	SortKey string `json:"-"`
}

type WorkspaceRelationship struct {
	ID           string               `json:"id"`
	SourceID     string               `json:"source_id"`
	TargetID     string               `json:"target_id"`
	TypeID       string               `json:"type_id,omitempty"`
	TypeName     string               `json:"type_name"`
	InverseLabel string               `json:"inverse_label,omitempty"`
	Directed     bool                 `json:"directed"`
	CreatedAt    time.Time            `json:"created_at"`
	SourceItem   RelationshipEndpoint `json:"source_item"`
	TargetItem   RelationshipEndpoint `json:"target_item"`
}

type WorkspaceRelationshipType struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Directed     bool   `json:"directed"`
	InverseLabel string `json:"inverse_label,omitempty"`
	// SortKey is the folded name type pages are ordered and resumed by.
	SortKey string `json:"-"`
}

// relationshipExploreSQL is shared by list, type discovery, item discovery and
// neighborhoods. Both endpoint ACLs and selected surfaces are checked in SQL.
func (r *Repo) relationshipExploreSQL(in RelationshipExploreQuery) (string, []any) {
	sourceSurface, sourceArgs := r.acl.Surface("src.id", in.Reader, "auto")
	targetSurface, targetArgs := r.acl.Surface("dst.id", in.Reader, "auto")
	sourceRead, sourceReadArgs := r.acl.Resource("src.id", in.Reader, access.Read)
	targetRead, targetReadArgs := r.acl.Resource("dst.id", in.Reader, access.Read)
	query := ` FROM relationships l
 JOIN resources src ON src.id=l.source_id
 JOIN resources dst ON dst.id=l.target_id
 JOIN item_surfaces sp ON sp.item_id=src.id AND sp.surface=` + sourceSurface + `
 JOIN item_surfaces tp ON tp.item_id=dst.id AND tp.surface=` + targetSurface + `
 JOIN resources sw ON sw.id=src.workspace_id
 JOIN resources tw ON tw.id=dst.workspace_id
 JOIN resources sc ON sc.id=src.container_id
 JOIN resources tc ON tc.id=dst.container_id
 LEFT JOIN relationship_types rt ON rt.id=l.type_id
 LEFT JOIN terms term ON term.id=l.type_id
 WHERE (src.workspace_id=? OR dst.workspace_id=?) AND ` + sourceRead + ` AND ` + targetRead
	args := append([]any{}, sourceArgs...)
	args = append(args, targetArgs...)
	args = append(args, in.WorkspaceID, in.WorkspaceID)
	args = append(args, sourceReadArgs...)
	args = append(args, targetReadArgs...)
	if in.ItemID != "" {
		query += " AND (l.source_id=? OR l.target_id=?)"
		args = append(args, in.ItemID, in.ItemID)
	}
	if in.CollectionID != "" {
		query += " AND ((src.workspace_id=? AND src.container_id=?) OR (dst.workspace_id=? AND dst.container_id=?))"
		args = append(args, in.WorkspaceID, in.CollectionID, in.WorkspaceID, in.CollectionID)
	}
	if in.TypeID != "" {
		query += " AND l.type_id=?"
		args = append(args, in.TypeID)
	}
	if in.Untyped {
		query += " AND l.type_id IS NULL"
	}
	if in.Directed != nil {
		query += " AND l.directed=?"
		args = append(args, *in.Directed)
	}
	switch in.Scope {
	case "internal":
		query += " AND src.workspace_id=? AND dst.workspace_id=?"
		args = append(args, in.WorkspaceID, in.WorkspaceID)
	case "external":
		query += " AND (src.workspace_id<>? OR dst.workspace_id<>?)"
		args = append(args, in.WorkspaceID, in.WorkspaceID)
	}
	switch in.Direction {
	case "incoming":
		query += " AND l.directed AND l.target_id=?"
		args = append(args, in.ItemID)
	case "outgoing":
		query += " AND l.directed AND l.source_id=?"
		args = append(args, in.ItemID)
	case "symmetric":
		query += " AND NOT l.directed"
	}
	return query, args
}

// ExploreWorkspaceRelationships loads a bounded page in one query, without full
// resource JSON or per-edge resource lookups. The caller requests one extra row.
func (r *Repo) ExploreWorkspaceRelationships(ctx context.Context, in RelationshipExploreQuery) ([]WorkspaceRelationship, error) {
	from, args := r.relationshipExploreSQL(in)
	if in.Filter != nil {
		predicate, values := r.linkFilter(in.Filter)
		from += " AND (" + predicate + ")"
		args = append(args, values...)
	}
	if in.After != "" {
		from += " AND l.id>?"
		args = append(args, in.After)
	}
	query := `SELECT l.id,l.source_id,l.target_id,coalesce(l.type_id,''),coalesce(term.name,'Untyped'),coalesce(rt.inverse_label,''),l.directed,l.created_at,
 src.id,sp.name,src.workspace_id,sw.name,src.container_id,sc.name,
 dst.id,tp.name,dst.workspace_id,tw.name,dst.container_id,tc.name` + from + ` ORDER BY l.id LIMIT ?`
	args = append(args, in.Limit)
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := []WorkspaceRelationship{}
	for rows.Next() {
		var l WorkspaceRelationship
		if err = rows.Scan(&l.ID, &l.SourceID, &l.TargetID, &l.TypeID, &l.TypeName, &l.InverseLabel, &l.Directed, &l.CreatedAt,
			&l.SourceItem.ID, &l.SourceItem.Name, &l.SourceItem.WorkspaceID, &l.SourceItem.WorkspaceName, &l.SourceItem.CollectionID, &l.SourceItem.CollectionName,
			&l.TargetItem.ID, &l.TargetItem.Name, &l.TargetItem.WorkspaceID, &l.TargetItem.WorkspaceName, &l.TargetItem.CollectionID, &l.TargetItem.CollectionName); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, dberr.Translate(rows.Err())
}

// ExploreRelationshipTypes pages policies with an authorized incident edge. The
// EXISTS probe avoids aggregating or counting every relationship in a workspace.
func (r *Repo) ExploreRelationshipTypes(ctx context.Context, in RelationshipExploreQuery) ([]WorkspaceRelationshipType, error) {
	in.TypeID = ""
	in.Untyped = false
	in.ItemID = ""
	in.Direction = ""
	from, args := r.relationshipExploreSQL(in)
	name := r.d.Fold("t.name")
	query := `SELECT catalog.id,t.name,catalog.directed,catalog.inverse_label,` + name + ` FROM relationship_types catalog JOIN terms t ON t.id=catalog.id WHERE EXISTS(SELECT 1` + from + ` AND l.type_id=catalog.id)`
	if search := refs.FoldName(strings.TrimSpace(in.Search)); search != "" {
		// The name, a localized label or a synonym contains the text.
		query += ` AND (` + r.d.Contains(name, "?") + ` OR EXISTS(SELECT 1 FROM ` + r.d.ObjectValues("t.labels", "label") + ` WHERE ` + r.d.Contains(r.d.Fold("label.value"), "?") + `) OR EXISTS(SELECT 1 FROM ` + r.d.Elements("t.synonyms", "synonym") + ` WHERE ` + r.d.Contains(r.d.Fold("synonym.value"), "?") + `))`
		args = append(args, search, search, search)
	}
	if in.After != "" {
		key, id, _ := strings.Cut(in.After, "\x1f")
		query += " AND (" + name + ">? OR (" + name + "=? AND catalog.id>?))"
		args = append(args, key, key, id)
	}
	query += " ORDER BY " + name + ",catalog.id LIMIT ?"
	args = append(args, in.Limit)
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := []WorkspaceRelationshipType{}
	for rows.Next() {
		var t WorkspaceRelationshipType
		if err = rows.Scan(&t.ID, &t.Name, &t.Directed, &t.InverseLabel, &t.SortKey); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, dberr.Translate(rows.Err())
}

// ExploreRelationshipItems searches the selected surfaces of actual endpoints,
// including external peers. EXISTS retains the anchor and both endpoint ACLs.
func (r *Repo) ExploreRelationshipItems(ctx context.Context, in RelationshipExploreQuery) ([]RelationshipEndpoint, error) {
	candidateID := in.ItemID
	in.ItemID = ""
	in.Direction = ""
	from, edgeArgs := r.relationshipExploreSQL(in)
	selected, surfaceArgs := r.acl.Surface("candidate.id", in.Reader, "auto")
	query := `SELECT candidate.id,cp.name,candidate.workspace_id,cw.name,candidate.container_id,cc.name,cp.folded_name
 FROM item_surfaces cp JOIN resources candidate ON candidate.id=cp.item_id
 JOIN resources cw ON cw.id=candidate.workspace_id JOIN resources cc ON cc.id=candidate.container_id
 WHERE cp.surface=` + selected
	args := surfaceArgs
	if candidateID != "" {
		query += " AND candidate.id=?"
		args = append(args, candidateID)
	}
	if search := refs.FoldName(strings.TrimSpace(in.Search)); search != "" {
		// An indexed prefix range avoids scanning every relationship for suggestions.
		query += " AND cp.folded_name>=? AND cp.folded_name<?"
		args = append(args, search, search+"\U0010ffff")
	}
	if in.After != "" {
		key, id, _ := strings.Cut(in.After, "\x1f")
		query += " AND (cp.folded_name>? OR (cp.folded_name=? AND candidate.id>?))"
		args = append(args, key, key, id)
	}
	query += ` AND EXISTS(SELECT 1` + from + ` AND (l.source_id=candidate.id OR l.target_id=candidate.id)) ORDER BY cp.folded_name,candidate.id LIMIT ?`
	args = append(args, edgeArgs...)
	args = append(args, in.Limit)
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := []RelationshipEndpoint{}
	for rows.Next() {
		var item RelationshipEndpoint
		if err = rows.Scan(&item.ID, &item.Name, &item.WorkspaceID, &item.WorkspaceName, &item.CollectionID, &item.CollectionName, &item.SortKey); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, dberr.Translate(rows.Err())
}

// ValidateRelationshipExplore rejects contradictory or unbounded public queries.
func ValidateRelationshipExplore(in RelationshipExploreQuery) error {
	if len(in.Search) > 256 {
		return fmt.Errorf("%w: search is limited to 256 bytes", dberr.ErrCheckViolation)
	}
	if in.Scope != "" && in.Scope != "all" && in.Scope != "internal" && in.Scope != "external" {
		return fmt.Errorf("%w: scope must be all, internal or external", dberr.ErrCheckViolation)
	}
	if in.Direction != "" && in.Direction != "both" && in.Direction != "incoming" && in.Direction != "outgoing" && in.Direction != "symmetric" {
		return fmt.Errorf("%w: direction must be both, incoming, outgoing or symmetric", dberr.ErrCheckViolation)
	}
	if (in.Direction == "incoming" || in.Direction == "outgoing") && in.ItemID == "" {
		return fmt.Errorf("%w: incoming and outgoing require item_id", dberr.ErrCheckViolation)
	}
	if in.Untyped && in.TypeID != "" {
		return fmt.Errorf("%w: untyped and type cannot be combined", dberr.ErrCheckViolation)
	}
	return nil
}
