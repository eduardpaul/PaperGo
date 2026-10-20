package aiops

import (
	"net/http"

	"papergo/internal/httpapi"
)

// Module serves the AI routes.
func (a *Assistant) Module() httpapi.Module { return module{a} }

type module struct{ a *Assistant }

func (module) Name() string { return "aiops" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodPost, Pattern: "/v1/ai/completions", OperationID: "startCompletion", Handle: m.complete},
		{Method: http.MethodPost, Pattern: "/v1/ai/decisions", OperationID: "startDecision", Handle: m.decide},
		{Method: http.MethodPost, Pattern: "/v1/ai/taxonomy-decisions", OperationID: "startTaxonomyDecision", Handle: m.taxonomy},
	}
}

func (m module) complete(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request CompletionRequest
	if !in.Decode(w, r, &request) {
		return nil
	}
	op, err := m.a.StartCompletion(r.Context(), in.DMS, in.Subject, request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (m module) decide(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request DecisionRequest
	if !in.Decode(w, r, &request) {
		return nil
	}
	op, err := m.a.StartDecision(r.Context(), in.DMS, in.Subject, request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (m module) taxonomy(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request TaxonomyDecisionRequest
	if !in.Decode(w, r, &request) {
		return nil
	}
	op, err := m.a.StartTaxonomyDecision(r.Context(), in.DMS, in.Subject, request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}
