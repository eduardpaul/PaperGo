package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
)

// controlFlowExec executes the actual step callbacks and JSON round trips for
// pure flow activities. Durable nested-loop integration lives in runner tests;
// exercising interpreter limits here needs no per-node journal transactions.
type controlFlowExec struct {
	ctx    context.Context
	calls  map[string]int
	events []dms.Event
}

func (x *controlFlowExec) Step(name string, fn func(context.Context, *dms.Service, func(dms.Event)) (json.RawMessage, error)) (json.RawMessage, error) {
	if err := x.ctx.Err(); err != nil {
		return nil, err
	}
	x.calls[name]++
	return fn(x.ctx, nil, func(event dms.Event) { x.events = append(x.events, event) })
}
func (*controlFlowExec) Work(name, activity string, _ func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected work %s (%s) in control-flow test", name, activity)
}
func (*controlFlowExec) Await(node string, _ Park) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected wait at %s in control-flow test", node)
}
func (*controlFlowExec) Sleep(time.Duration) error {
	return errors.New("unexpected sleep in control-flow test")
}

func executeControlFlow(t *testing.T, raw string) (Result, *controlFlowExec, error) {
	t.Helper()
	def, err := ParseDefinition(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = def.validate(); err != nil {
		t.Fatal(err)
	}
	x := &controlFlowExec{ctx: t.Context(), calls: map[string]int{}}
	state := &runState{in: RunInput{Event: dms.Event{Type: TriggerManual}}, x: x, l: loaded{Key: "test", Definition: def}, vars: map[string]any{}, steps: map[string]any{}}
	result, err := state.execute(t.Context())
	return result, x, err
}

// More nodes than the per-pass limit can complete across separate passes.
func TestForEachPassesHaveTheirOwnNodeBudget(t *testing.T) {
	items := make([]string, 600)
	for i := range items {
		items[i] = strconv.Itoa(i)
	}
	result, x, err := executeControlFlow(t, `{
  "triggers":[{"type":"manual"}],
  "flow":{"start":"loop","nodes":{
   "loop":{"activity":"forEach","inputs":{"items":[`+strings.Join(items, ",")+`],"as":"n"},"next":{"body":"a"}},
   "a":{"activity":"set_variable","inputs":{"name":"last","value":"{var:n}"},"next":{"done":"b"}},
   "b":{"activity":"set_variable","inputs":{"name":"passes","value":"{var:n_index}"}}
  }}
 }`)
	if err != nil || result.Status != "completed" {
		t.Fatal(result, err)
	}
	if x.calls["a"] != len(items) || x.calls["b"] != len(items) || x.calls["a"]+x.calls["b"] <= MaxNodeVisits {
		t.Fatal("loop must execute every body beyond one pass's node budget", x.calls)
	}
	for _, key := range []string{"last", "passes"} {
		if got := fmt.Sprint(result.Variables[key]); got != "599" {
			t.Fatalf("%s=%s", key, got)
		}
	}
	if len(x.events) != 1 || x.events[0].Type != WorkflowEvent("test", "completed") {
		t.Fatal(x.events)
	}
}

func TestACycleStopsAtTheNodeLimit(t *testing.T) {
	for _, inside := range []bool{false, true} {
		name := "outside loop"
		if inside {
			name = "inside loop"
		}
		t.Run(name, func(t *testing.T) {
			start, loop := "a", ""
			if inside {
				start = "loop"
				loop = `"loop":{"activity":"forEach","inputs":{"items":[1],"as":"n"},"next":{"body":"a"}},`
			}
			result, x, err := executeControlFlow(t, `{
    "triggers":[{"type":"manual"}],
    "flow":{"start":"`+start+`","nodes":{`+loop+`
     "a":{"activity":"set_variable","inputs":{"name":"x","value":"1"},"next":{"done":"b"}},
     "b":{"activity":"set_variable","inputs":{"name":"x","value":"2"},"next":{"done":"a"}}
    }}
   }`)
			if !errors.Is(err, ErrRunFailed) || result.Status != "failed" || !strings.Contains(err.Error(), "without starting a loop pass") {
				t.Fatal(result, err)
			}
			if got := x.calls["a"] + x.calls["b"]; got != MaxNodeVisits {
				t.Fatalf("cycle executed %d nodes, want %d", got, MaxNodeVisits)
			}
			if len(x.events) != 1 || x.events[0].Type != WorkflowEvent("test", "failed") {
				t.Fatal(x.events)
			}
		})
	}
}
