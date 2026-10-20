package httpapi

import (
	"net/http"
	"papergo/internal/dms"
	"time"
)

func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := dms.AuditQuery{Actor: q.Get("actor"), EntityType: q.Get("entity_type"), EntityID: q.Get("entity_id"), WorkspaceID: q.Get("workspace_id"), After: q.Get("$skiptoken"), Limit: queryInt(r, "$top")}
	for key, target := range map[string]**time.Time{"from": &in.From, "to": &in.To} {
		if value := q.Get(key); value != "" {
			date, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				a.problem(w, r, 400, "invalid_audit_query", key+" must be an RFC3339 date")
				return
			}
			*target = &date
		}
	}
	out, err := a.dms(r).OrganizationAudit(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
