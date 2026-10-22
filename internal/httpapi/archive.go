package httpapi

import (
	"net/http"
	"papergo/internal/dms"
	"time"
)

func (a *API) archiveAction(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, ok := a.version(w, r)
		if !ok {
			return
		}
		out, err := a.dms(r).SetArchived(r.Context(), subject(r), r.PathValue("id"), version, archived)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		a.respondWithAccess(w, r, out)
	}
}
func (a *API) archiveItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := dms.ArchiveQuery{WorkspaceID: q.Get("workspace_id"), CollectionID: q.Get("collection_id"), ContentTypeID: q.Get("content_type_id"), Search: q.Get("q"), Keyword: q.Get("keyword"), ArchivedBy: q.Get("archived_by"), After: q.Get("$skiptoken"), Limit: queryInt(r, "$top")}
	for key, target := range map[string]*time.Time{"archived_from": &in.From, "archived_to": &in.To} {
		if value := q.Get(key); value != "" {
			var err error
			*target, err = time.Parse(time.RFC3339, value)
			if err != nil {
				a.failure(w, r, dms.Invalid(key+" must be an RFC3339 timestamp"))
				return
			}
		}
	}
	out, err := a.dms(r).ArchiveItems(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, http.StatusOK, out)
}
