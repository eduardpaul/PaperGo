package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"papergo/internal/ai"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/workflow"
	"papergo/internal/workflowai"
)

// Every source/format combination must reach an ordinary item.update, for
// both execution modes. In particular, chunk references must retain their
// full text rather than depending on the reader's bounded preview.
func TestLLMInputsAndOutputsPopulateFields(t *testing.T) {
	doctexttest.PDF(t)
	chatServer, _ := aiTools(t)
	batchServer, _, configure := batchTools(t, false)
	chat, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderOpenAI, Endpoint: chatServer.Endpoint(), Model: "fake-chat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: batchServer.Endpoint(), Model: "gpt-batch"}, batchServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, configure, func(tools *workflow.Tools) { workflowai.Configure(tools, workflowai.Runtime{Chat: chat, Batch: batch}) })
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "LLM inputs", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// These manual workflows exercise text extraction and chunk snapshots
	// themselves. Keep automatic indexing and previews from processing the
	// same uploads alongside each case.
	f.searchOff(t)
	for _, key := range []string{"search.content", "documents.preview"} {
		w := f.builtIn(t, key, lib.ID)
		if _, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, key, workflow.SetBuiltIn{CollectionID: lib.ID, Version: w.Version, AutomaticEnabled: false}); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []dms.CreateField{{Key: "source", Label: "Source", Type: "text"}, {Key: "answer", Label: "Answer", Type: "text"}, {Key: "amount", Label: "Amount", Type: "number"}} {
		if _, err = f.dms.CreateField(ctx, "alice", lib.ID, field); err != nil {
			t.Fatal(err)
		}
	}
	coordinator := f.enableBatch(t, map[string]any{"policy": "threshold", "batch_size": 1})
	for _, mode := range []string{"immediate", "batch"} {
		for _, source := range []string{"field", "document", "chunk"} {
			for _, structured := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", mode, source, structured), func(t *testing.T) {
					file := f.upload(t, lib.ID, fmt.Sprintf("%s-%s-%v.pdf", mode, source, structured), "application/pdf", doctexttest.TextPDF("Bakery bread total 12.50 EUR"))
					current := f.head(t, file.ID)
					values := map[string]any{"source": "Bakery bread total 12.50 EUR"}
					if _, err := f.dms.Update(ctx, "alice", file.ID, current.Version, dms.UpdateResource{Values: &values}); err != nil {
						t.Fatal(err)
					}
					ask := map[string]any{"prompt": fmt.Sprintf("Extract %s %s %v", mode, source, structured), "execution": mode}
					nodes := map[string]any{}
					start := "ask"
					switch source {
					case "field":
						ask["input"] = "{item:values.source}"
					case "document":
						start = "read"
						nodes["read"] = map[string]any{"activity": "document.read_text", "next": map[string]any{"done": "ask"}}
						ask["text_id"] = "{step:read.text_id}"
						ask["blob_id"] = "{step:read.blob_id}"
					case "chunk":
						start = "chunk"
						nodes["chunk"] = map[string]any{"activity": "search.chunk", "inputs": map[string]any{"text": "{item:values.source}", "chunker": "whole"}, "next": map[string]any{"done": "chunks"}}
						nodes["chunks"] = map[string]any{"activity": "search.read_chunks", "inputs": map[string]any{"generation": "{step:chunk.generation}"}, "next": map[string]any{"done": "ask"}}
						ask["chunk"] = "{step:chunks.chunks.0.chunk}"
						// Publishing another generation drops the original
						// search chunks. The LLM must still read its snapshot.
						nodes["chunks"].(map[string]any)["next"] = map[string]any{"done": "other"}
						nodes["other"] = map[string]any{"activity": "search.chunk", "inputs": map[string]any{"text": "Unrelated replacement text", "chunker": "whole"}, "next": map[string]any{"done": "publish"}}
						nodes["publish"] = map[string]any{"activity": "search.publish", "inputs": map[string]any{"generation": "{step:other.generation}"}, "next": map[string]any{"done": "ask"}}

					}
					result := map[string]any{"answer": "{step:ask.text}"}
					if structured {
						ask["output_schema"] = map[string]any{"type": "object", "required": []string{"total"}, "properties": map[string]any{"total": map[string]any{"type": "number"}}}
						result = map[string]any{"amount": "{step:ask.json.total}"}
					}
					nodes["ask"] = map[string]any{"activity": "ai.complete", "inputs": ask, "next": map[string]any{"done": "save"}}
					nodes["save"] = map[string]any{"activity": "item.update", "inputs": map[string]any{"values": result}}
					definition, _ := json.Marshal(map[string]any{"triggers": []any{map[string]any{"type": "manual", "collection_id": lib.ID}}, "flow": map[string]any{"start": start, "nodes": nodes}})
					w := f.workflow(t, fmt.Sprintf("%s-%s-%v", mode, source, structured), string(definition))
					ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{file.ID}})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.r.Dispatch(ctx); err != nil {
						t.Fatal(err)
					}
					if mode == "batch" {
						f.status(t, ids[0], "waiting")
						f.coordinate(t, coordinator.ID)
					}
					if run := f.wait(t, ids[0], settled); run.Status != "completed" {
						t.Fatalf("run failed: %s; steps %+v", run.Error, run.Steps)
					}
					got := f.head(t, file.ID)
					if structured {
						want := "42"
						if mode == "batch" {
							want = "1.25"
						}
						if shown(got.Values["amount"]) != want {
							t.Fatalf("structured field: %+v", got.Values)
						}
					} else if !strings.Contains(shown(got.Values["answer"]), "Bakery bread") {
						t.Fatalf("text field: %+v", got.Values)
					}
				})
			}
		}
	}
}
