package workflow

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

const IntegrationRefreshBuiltIn = "integrations.refresh"

func init() {
	register(&Activity{Key: "integration.refresh", Kind: "action", Description: "Queues refreshes of active list integrations in this workspace, or one integration.",
		InputSchema:  forms.MustParse(`{"type":"object","properties":{"integration_id":{"type":"string","title":"Integration ID"}}}`),
		OutputSchema: forms.MustParse(`{"type":"object","properties":{"queued":{"type":"integer"}}}`),
		run: func(e *env, in map[string]any) (outcome, error) {
			id := ""
			var err error
			if v, ok := in["integration_id"]; ok {
				id, err = e.scope.text(v)
				if err != nil {
					return outcome{}, err
				}
			}
			w, err := e.t.Client.Workflow.Get(e.ctx, e.workflowID)
			if err != nil {
				return outcome{}, err
			}
			var n int
			if w.BuiltinKey != nil && *w.BuiltinKey == IntegrationRefreshBuiltIn {
				n, err = e.t.QueueScheduledIntegrationRefreshes(e.ctx, w.ID)
			} else {
				n, err = e.t.QueueIntegrationRefreshes(e.ctx, e.author, e.workspaceID, id)
			}
			return outcome{Output: map[string]any{"queued": n}}, err
		}})
	registerBuiltIn(&BuiltIn{Key: IntegrationRefreshBuiltIn, Name: "Refresh list integrations", Description: "Every 15 minutes in UTC, refreshes active HTTPS iCalendar integrations using each integration's configuring user's current list permissions.",
		ParametersSchema: forms.MustParse(`{"type":"object","properties":{}}`),
		definition:       `{"triggers":[{"type":"schedule","cron":"*/15 * * * *","time_zone":"UTC"}],"flow":{"start":"refresh","nodes":{"refresh":{"activity":"integration.refresh"}}}}`})
}

// EnsureIntegrationWorkflow activates the workspace schedule once, atomically
// with adding a list integration. List Manage is sufficient; an existing
// disabled workflow is respected, and is never silently enabled again.
func (s *Service) EnsureIntegrationWorkflow(ctx context.Context, t *dms.Service, subject string, list *ent.Resource) error {
	if _, err := t.Authorize(ctx, subject, list.ID, "manage"); err != nil {
		return err
	}
	b := builtins[IntegrationRefreshBuiltIn]
	existing, err := s.builtInRow(ctx, t, list.WorkspaceID, b, "")
	if err != nil || existing != nil {
		return err
	}
	raw, err := b.fill(map[string]any{})
	if err != nil {
		return err
	}
	def, raw, err := s.check(ctx, t, subject, list.WorkspaceID, raw)
	if err != nil {
		return err
	}
	key, err := freeKey(ctx, t, list.WorkspaceID, keyFromName(b.Key))
	if err != nil {
		return err
	}
	w, err := t.Client.Workflow.Create().SetWorkspaceID(list.WorkspaceID).SetKey(key).SetName(b.Name).SetDescription(b.Description).SetEnabled(true).SetBuiltinKey(b.Key).SetParameters(map[string]any{}).SetCurrentVersion(1).SetCreatedBy(subject).SetUpdatedBy(subject).Save(ctx)
	if err != nil {
		return err
	}
	if err = t.Client.WorkflowVersion.Create().SetWorkflowID(w.ID).SetNumber(1).SetDefinition(json.RawMessage(raw)).SetCreatedBy(subject).Exec(ctx); err != nil {
		return err
	}
	if err = index(ctx, t, w, def); err != nil {
		return err
	}
	return audit(ctx, t, subject, "workflow.builtin", w, map[string]any{"workflow_id": w.ID, "builtin_key": b.Key, "enabled": true, "version": 1})
}
