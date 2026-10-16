package workflowai

import (
	"time"

	"papergo/internal/ai"
	"papergo/internal/workflow"
)

// Runtime is supplied by the host; the runner has no AI-specific dependencies.
type Runtime struct {
	Chat             ai.Chat
	Batch            ai.Batcher
	DefaultExecution string
	BatchDeadline    time.Duration
}

const runtimeKey = "papergo.ai"

func Configure(t *workflow.Tools, r Runtime) {
	if t.Extensions == nil {
		t.Extensions = map[string]any{}
	}
	t.Extensions[runtimeKey] = r
}
func runtime(t workflow.Tools) Runtime { r, _ := t.Extensions[runtimeKey].(Runtime); return r }
