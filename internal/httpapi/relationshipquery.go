package httpapi

import (
	"errors"
	"net/http"
	"papergo/internal/dms"
	"strconv"
)

func (a *API) workspaceRelationships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var directed *bool
	if values, exists := q["directed"]; exists {
		if len(values) != 1 || values[0] != "true" && values[0] != "false" {
			a.problem(w, r, 400, "invalid_relationship_query", "directed must be true or false")
			return
		}
		value, _ := strconv.ParseBool(values[0])
		directed = &value
	}
	out, err := a.DMS.WorkspaceRelationships(r.Context(), subject(r), r.PathValue("id"), q.Get("type"), directed, q.Get("$filter"), q.Get("$skiptoken"), queryInt(r, "$top"))
	if err != nil {
		var validation *dms.ValidationError
		if errors.As(err, &validation) {
			a.problem(w, r, 400, "invalid_relationship_query", validation.Message)
			return
		}
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
