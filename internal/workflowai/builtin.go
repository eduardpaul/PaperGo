package workflowai

import (
	"context"
	"errors"

	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterBuiltIn(&workflow.BuiltIn{
		Key:         builtinKey,
		Name:        "AI batch coordinator",
		Description: "Each minute, admits one named queue of parked AI requests for the installation: configurable groups, a daily partial flush, and provider polling. Originating workflows keep their own authors.",
		System:      true,
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"batch_size": {"type":"integer","title":"Batch size","minimum":1,"maximum":100,"default":20},
			"queue": {"type": "string", "title": "Queue", "minLength": 1, "maxLength": 64, "default": "shared", "description": "Named queue this coordinator admits."},
			"policy": {"type": "string", "title": "Admission", "default": "both", "description": "threshold submits full groups. schedule submits on the flush, including a partial group. both does both."},
			"flush_cron": {"type": "string", "title": "Flush schedule", "default": "0 1 * * *", "description": "When a partial group is sent."},
			"time_zone": {"type": "string", "title": "Time zone", "default": "Europe/Madrid", "description": "Time zone of the flush."}
		}}`),
		Definition: `{
			"triggers": [
				{"type": "schedule", "cron": "* * * * *", "time_zone": "UTC"},
				{"type": "manual"}
			],
			"flow": {"start": "coordinate", "nodes": {"coordinate": {"activity": "ai.batch.coordinate", "inputs": {
				"batch_size":"{param:batch_size}","queue": "{param:queue}", "policy": "{param:policy}", "flush_cron": "{param:flush_cron}", "time_zone": "{param:time_zone}"
			}}}}
		}`,
	})
}

// EnsureCoordinator turns the AI batch coordinator on in workspaceID.
// An existing workflow, including one that was turned off, is left as it is.
// The subject becomes the workflow version's author and must manage the
// workspace; each run also needs organization.manage.
func EnsureCoordinator(ctx context.Context, workflows *workflow.Service, subject, workspaceID string) error {
	if workflows == nil || workspaceID == "" {
		return nil
	}
	_, err := workflows.UpdateBuiltIn(ctx, subject, workspaceID, builtinKey, workflow.SetBuiltIn{AutomaticEnabled: true})
	if errors.Is(err, dms.ErrPreconditionFailed) {
		return nil
	}
	return err
}
