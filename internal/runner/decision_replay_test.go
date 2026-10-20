package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"papergo/internal/dal/resources"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal/resultcache"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

const decisionCrashEnv = "PAPERGO_TEST_DECISION_CRASH"

type crashDecisionCache struct{ resultcache.Cache }

func (c crashDecisionCache) PutIfAbsent(ctx context.Context, namespace, scope, key string, payload json.RawMessage, ttl time.Duration) (json.RawMessage, error) {
	winner, err := c.Cache.PutIfAbsent(ctx, namespace, scope, key, payload, ttl)
	if err == nil && namespace == "ai.decide" {
		os.Exit(32)
	}
	return winner, err
}
func TestDecisionCrashChild(t *testing.T) {
	spec := os.Getenv(decisionCrashEnv)
	if spec == "" {
		return
	}
	parts := strings.Split(spec, "|")
	phase := parts[0]
	model := &runnerDecisionModel{probability: .9}
	if phase == "prepare" {
		model.during = func() { os.Exit(31) }
	}
	f := open(t, parts[1], "decision-child", func(tools *workflow.Tools) {
		tools.Decisions = model
		if phase == "inference" {
			tools.Results = crashDecisionCache{tools.Results}
		}
	})
	f.ws = &resources.Resource{ID: parts[2]}
	f.list = &resources.Resource{ID: parts[3]}
	ids, err := f.wf.StartRuns(ctx, "alice", parts[4], workflow.Start{ItemIDs: []string{parts[5]}, Inputs: map[string]any{"amount": json.Number("9007199254740993")}})
	if err != nil {
		t.Fatal(err)
	}
	if phase == "commit" {
		f.status(t, ids[0], "waiting")
		os.Exit(33)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("decision did not reach crash boundary")
}
func TestDecisionDurableCrashRecovery(t *testing.T) {
	for i, phase := range []string{"prepare", "inference", "commit"} {
		t.Run(phase, func(t *testing.T) {
			f := setup(t)
			w := f.workflow(t, "Durable decision", `{"triggers":[{"type":"manual","collection_id":"$LIST"}],"input_schema":{"type":"object","properties":{"amount":{"type":"integer"}}},"flow":{"start":"get","nodes":{
   "get":{"activity":"item.get","next":{"done":"decide"}},
   "decide":{"activity":"ai.decide","inputs":{"data":{"amount":"{input:amount}"},"questions":[{"key":"route","instructions":"Which route?","options":[{"key":"other","text":"Other document"},{"key":"billing","text":"Billing"}]}]},"next":{"done":"review"}},
   "review":{"activity":"approval","inputs":{"title":"Review decision","assignees":["bob"]},"next":{"approved":"apply"}},
   "apply":{"activity":"item.update","inputs":{"values":{"note":"{step:decide.answers.route.selected}"},"expected_version":"{step:get.version}"}}
  }}}`)
			item := f.item(t, "alice", "Reviewed invoice", nil)
			if err := f.r.Shutdown(10 * time.Second); err != nil {
				t.Fatal(err)
			}
			f.db.Close()
			child := exec.Command(os.Args[0], "-test.run=^TestDecisionCrashChild$", "-test.count=1")
			child.Env = append(os.Environ(), decisionCrashEnv+"="+strings.Join([]string{phase, f.path, f.ws.ID, f.list.ID, w.ID, item.ID}, "|"))
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 31+i {
				t.Fatalf("unexpected crash: %v\n%s", err, output)
			}
			model := &runnerDecisionModel{probability: .9}
			recovered := open(t, f.path, "decision-child", func(tools *workflow.Tools) { tools.Decisions = model })
			recovered.ws, recovered.list = f.ws, f.list
			runs := recovered.runs(t, w.ID, 1)
			task := recovered.approvalOf(t, runs[0].ID)
			if _, err = recovered.tasks().Respond(ctx, "bob", task.ID, tasks.Response{Outcome: "approved"}); err != nil {
				t.Fatal(err)
			}
			recovered.status(t, runs[0].ID, "completed")
			if recovered.head(t, item.ID).Values["note"] != "billing" {
				t.Fatal("recovered decision was not consumed")
			}
			expectedCalls := int32(0)
			if phase == "prepare" {
				expectedCalls = 1
			}
			if model.calls.Load() != expectedCalls {
				t.Fatal("repeated completed inference", phase, model.calls.Load())
			}
			if phase == "prepare" {
				model.mu.Lock()
				state := model.states[0]
				model.mu.Unlock()
				if !strings.Contains(state, "9007199254740993") {
					t.Fatal("replay rounded JSON numbers", state)
				}
			}
		})
	}
}
