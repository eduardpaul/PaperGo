package dms

import (
	"context"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
	"sort"
)

// countedGroups answers the group counts of a whole collection by a
// single-choice field from group_counts, which triggers keep current, instead
// of reading every item. It applies when nothing narrows the query and the
// caller reads every item on the same surface; otherwise ok is false and the
// query counts the rows it may read.
func (s *Service) countedGroups(ctx context.Context, subject, collection string, in QueryRequest, d *catalog.FieldDefinition) (groups []QueryGroup, ok bool, err error) {
	spec := in.Query
	if in.After != "" || in.rowACL || spec.Filter != nil || spec.Search != "" || spec.Keyword != "" || spec.ContentTypeID != "" || spec.ParentID != "" || spec.AncestorID != "" {
		return nil, false, nil
	}
	if d == nil || d.Type != catalog.FieldChoice || d.Options.Multiple {
		return nil, false, nil
	}
	c, err := s.DAL.Resources().Resource(ctx, collection)
	if err != nil {
		return nil, false, err
	}
	scopes, err := s.DAL.Content().Scopes(ctx, collection, principal(ctx, subject))
	if err != nil || scopes == nil {
		return nil, false, err
	}
	surface, ok := scopes.Uniform(in.Surface)
	if !ok {
		return nil, false, nil
	}
	if !c.PublishingEnabled {
		surface = "published"
	}
	counts, err := s.DAL.Surfaces().Counts(ctx, collection, refs.Surface(surface), d.Key)
	if err != nil {
		return nil, false, err
	}
	total, counted := counts.Total, 0
	groups = []QueryGroup{}
	for _, c := range counts.Values {
		groups = append(groups, QueryGroup{Value: c.Value, Count: c.Count})
		counted += c.Count
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
