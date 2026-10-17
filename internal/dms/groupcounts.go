package dms

import (
	"context"
	"errors"
	"sort"

	"papergo/ent"
)

// countedGroups answers the group counts of a whole collection by a
// single-choice field from group_counts, which triggers keep current, instead
// of reading every item. It applies when nothing narrows the query and the
// caller reads every item on the same surface; otherwise ok is false and the
// query counts the rows it may read.
func (s *Service) countedGroups(ctx context.Context, subject, collection string, in QueryRequest, d *ent.FieldDefinition) (groups []QueryGroup, ok bool, err error) {
	spec := in.Query
	if in.After != "" || in.rowACL || spec.Filter != nil || spec.Search != "" || spec.Keyword != "" || spec.ContentTypeID != "" || spec.ParentID != "" || spec.AncestorID != "" {
		return nil, false, nil
	}
	if d == nil || d.Type != "choice" || d.Options.Multiple {
		return nil, false, nil
	}
	c, err := s.Client.Resource.Get(ctx, collection)
	if err != nil {
		return nil, false, err
	}
	access, err := s.collectionScopes(ctx, collection, subject)
	if err != nil || access == nil {
		return nil, false, err
	}
	surface, ok := access.uniform(in.Surface)
	if !ok {
		return nil, false, nil
	}
	if !c.PublishingEnabled {
		surface = "published"
	}
	rows, err := s.Client.QueryContext(ctx, `SELECT field_key,value,count FROM group_counts WHERE container_id=? AND surface=? AND field_key IN ('',?) AND count>0`, collection, surface, d.Key)
	if err != nil {
		return nil, false, err
	}
	total, counted := 0, 0
	groups = []QueryGroup{}
	for rows.Next() {
		var key, value string
		var n int
		if err = rows.Scan(&key, &value, &n); err != nil {
			break
		}
		if key == "" {
			total = n
			continue
		}
		groups = append(groups, QueryGroup{Value: value, Count: n})
		counted += n
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, false, err
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Value.(string) < groups[j].Value.(string) })
	if missing := total - counted; missing > 0 {
		groups = append(groups, QueryGroup{Value: nil, Count: missing})
	}
	if len(groups) > pageSize(in.Limit) {
		return nil, false, nil
	}
	return groups, true, nil
}
