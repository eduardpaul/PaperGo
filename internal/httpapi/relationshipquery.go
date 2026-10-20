package httpapi

import (
	"errors"
	"net/http"
	"papergo/internal/dms"
	"strconv"
)

func (a *API) workspaceRelationshipQuery(w http.ResponseWriter, r *http.Request) (dms.WorkspaceRelationshipsQuery, bool) {
	q := r.URL.Query()
	in := dms.WorkspaceRelationshipsQuery{Type: q.Get("type"), ItemID: q.Get("item_id"), CollectionID: q.Get("collection_id"), Scope: q.Get("scope"), Direction: q.Get("direction"), Search: q.Get("q"), Filter: q.Get("$filter"), After: q.Get("$skiptoken"), Limit: queryInt(r, "$top")}
	for key, target := range map[string]**bool{"directed": &in.Directed} {
		if values, exists := q[key]; exists {
			if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
				a.problem(w, r, 400, "invalid_relationship_query", key+" must be true or false")
				return in, false
			}
			value, _ := strconv.ParseBool(values[0])
			*target = &value
		}
	}
	if values, exists := q["untyped"]; exists {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			a.problem(w, r, 400, "invalid_relationship_query", "untyped must be true or false")
			return in, false
		}
		in.Untyped = values[0] == "true"
	}
	return in, true
}

func (a *API) relationshipResult(w http.ResponseWriter, r *http.Request, out any, err error) {
	if err != nil {
		var validation *dms.ValidationError
		if errors.As(err, &validation) {
			a.problem(w, r, 400, "invalid_relationship_query", validation.Message)
		} else {
			a.failure(w, r, err)
		}
		return
	}
	respond(w, r, 200, out)
}
func (a *API) workspaceRelationships(w http.ResponseWriter, r *http.Request) {
	in, ok := a.workspaceRelationshipQuery(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.WorkspaceRelationships(r.Context(), subject(r), r.PathValue("id"), in)
	a.relationshipResult(w, r, out, err)
}
func (a *API) workspaceRelationshipTypes(w http.ResponseWriter, r *http.Request) {
	in, ok := a.workspaceRelationshipQuery(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.WorkspaceRelationshipTypes(r.Context(), subject(r), r.PathValue("id"), in)
	a.relationshipResult(w, r, out, err)
}
func (a *API) workspaceRelationshipItems(w http.ResponseWriter, r *http.Request) {
	in, ok := a.workspaceRelationshipQuery(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.WorkspaceRelationshipItems(r.Context(), subject(r), r.PathValue("id"), in)
	a.relationshipResult(w, r, out, err)
}
func (a *API) workspaceRelationshipNeighborhood(w http.ResponseWriter, r *http.Request) {
	in, ok := a.workspaceRelationshipQuery(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.WorkspaceRelationshipNeighborhood(r.Context(), subject(r), r.PathValue("id"), in)
	a.relationshipResult(w, r, out, err)
}
