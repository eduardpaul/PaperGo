package builtin

import (
	"net/url"

	"papergo/internal/dal/refs"
)

// links are the web app's addresses of what the tools return, so an
// assistant can send people to it. Without an app origin there are none.
type links struct{ app *url.URL }

func (l links) to(path string, query url.Values) *string {
	if l.app == nil {
		return nil
	}
	u := *l.app
	u.Path, u.RawQuery = path, query.Encode()
	s := u.String()
	return &s
}

func (l links) workspace(id string) *string { return l.to("/w/"+url.PathEscape(id), nil) }

func (l links) collection(workspace, id string) *string {
	return l.to("/w/"+url.PathEscape(workspace)+"/l/"+url.PathEscape(id), nil)
}

// entry opens a folder, or an item in the folder that holds it.
func (l links) entry(r resource) *string {
	if r.ContainerID == nil {
		return nil
	}
	return l.at(r.Kind, r.WorkspaceID, *r.ContainerID, r.ParentID, r.ID)
}

func (l links) at(kind refs.ResourceKind, workspace, collection string, parent *string, id string) *string {
	q := url.Values{}
	switch {
	case kind == refs.KindFolder:
		q.Set("folder", id)
	case parent != nil && *parent != collection:
		q.Set("folder", *parent)
		q.Set("item", id)
	default:
		q.Set("item", id)
	}
	return l.to("/w/"+url.PathEscape(workspace)+"/l/"+url.PathEscape(collection), q)
}
