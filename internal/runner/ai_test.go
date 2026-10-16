package runner

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/workflow"
	"papergo/internal/workflowai"
)

// sample creates a workflow of samples/workflows for a library.
func (f *fixture) sample(t *testing.T, name, libraryID string) workflow.Workflow {
	t.Helper()
	raw, err := os.ReadFile("../../samples/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var save workflow.Save
	if err = json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "$LIBRARY", libraryID)), &save); err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.Create(ctx, "alice", f.ws.ID, save)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return w
}

func aiTools(t *testing.T) (*aitest.Server, func(*workflow.Tools)) {
	server := aitest.New(t)
	embeddings, err := ai.NewEmbedder(ai.EmbeddingsConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "fake-embed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "fake-chat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, func(tools *workflow.Tools) {
		tools.Embeddings = embeddings
		workflowai.Configure(tools, workflowai.Runtime{Chat: chat})
	}
}

// The receipts sample adds a model-written summary chunk to each receipt;
// an identical receipt reuses the cached summary.
func TestReceiptsSampleSummarizesOnce(t *testing.T) {
	doctexttest.PDF(t)
	server, tools := aiTools(t)
	f := setup(t, tools)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Receipts", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.sample(t, "receipts.json", lib.ID)
	receipt := doctexttest.TextPDF("Corner Bakery 2026-10-02 two loaves sourdough total 12.50 EUR")
	first := f.upload(t, lib.ID, "bakery.pdf", "application/pdf", receipt)
	content := f.waitContent(t, first.ID, first.Blob.ID)
	if content.Chunks != 2 {
		t.Fatalf("receipt and summary chunks: %+v", content)
	}
	if n := server.Chats.Load(); n != 1 {
		t.Fatalf("%d chat requests", n)
	}
	// The summary chunk is searched, off the pages.
	f.searchNames(t, "alice", dms.SearchRequest{Q: "summarize sourdough", Surface: "head", Mode: "keyword"}, "bakery.pdf")
	found, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "summarize", Surface: "head", Mode: "keyword"})
	if err != nil || len(found.Hits) != 1 || found.Hits[0].Page != 0 || !strings.HasPrefix(found.Hits[0].Snippet, "Summarize: Corner Bakery") {
		t.Fatalf("summary hit: %+v %v", found.Hits, err)
	}

	// The same receipt again: text, summary and vectors are reused.
	embedded := server.Inputs.Load()
	second := f.upload(t, lib.ID, "bakery-copy.pdf", "application/pdf", receipt)
	f.waitContent(t, second.ID, second.Blob.ID)
	if n := server.Chats.Load(); n != 1 {
		t.Fatalf("the cached summary was asked again: %d chat requests", n)
	}
	if n := server.Inputs.Load(); n != embedded {
		t.Fatalf("equal chunks were embedded again: %d inputs, was %d", n, embedded)
	}
}

// The contracts sample splits contracts at headings and gives each section
// model-written context from the document; the same contract again asks the
// model nothing.
func TestContractsSampleEnrichesSections(t *testing.T) {
	doctexttest.PDF(t)
	server, tools := aiTools(t)
	f := setup(t, tools)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Contracts", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.sample(t, "contracts.json", lib.ID)
	contract := doctexttest.TextPDF("LEASE AGREEMENT\n1. PARTIES\nAcme Ltd leases the office to Beta GmbH.\n2. RENT\nRent is due monthly.", "3. TERMINATION\nEither party may end the lease with notice.")
	file := f.upload(t, lib.ID, "lease.pdf", "application/pdf", contract)
	content := f.waitContent(t, file.ID, file.Blob.ID)
	chunks, err := f.dms.SearchStore().GenerationChunks(ctx, content.Generation)
	if err != nil || len(chunks) < 3 {
		t.Fatalf("sections: %+v %v", chunks, err)
	}
	for _, c := range chunks {
		// The fake model answers with the instructions' first word and the
		// beginning of its input: the document, then the passage.
		if !strings.HasPrefix(c.Enrichment, "In: <document> 1. PARTIES Acme Ltd") {
			t.Fatalf("enrichment: %+v", c)
		}
	}
	asked := server.Chats.Load()
	if asked != int64(len(chunks)) {
		t.Fatalf("%d chat requests for %d sections", asked, len(chunks))
	}
	query := dms.SearchRequest{Q: "termination notice", Surface: "head", Mode: "keyword"}
	f.searchNames(t, "alice", query, "lease.pdf")
	termination, err := f.dms.Search(ctx, "alice", query)
	if err != nil || len(termination.Hits) != 1 || termination.Hits[0].Page != 2 {
		t.Fatalf("section hit: %+v %v", termination.Hits, err)
	}

	copy := f.upload(t, lib.ID, "lease-copy.pdf", "application/pdf", contract)
	f.waitContent(t, copy.ID, copy.Blob.ID)
	if n := server.Chats.Load(); n != asked {
		t.Fatalf("cached enrichments were asked again: %d chat requests, was %d", n, asked)
	}
}

// ai.complete answers JSON matching an output_schema, which later steps
// read, and asks the model once for equal requests.
func TestAICompleteJSON(t *testing.T) {
	server, tools := aiTools(t)
	f := setup(t, tools)
	f.searchOff(t)
	w := f.workflow(t, "Extract", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}], "flow": {"start": "extract", "nodes": {
		"extract": {"activity": "ai.complete", "inputs": {"prompt": "Extract the vendor and total.", "input": "Corner Bakery total 12.50",
			"output_schema": {"type": "object", "required": ["vendor", "total"], "properties": {"vendor": {"type": "string"}, "total": {"type": "number"}}}},
			"next": {"done": "check"}},
		"check": {"activity": "if", "inputs": {"left": "{step:extract.json.vendor}", "op": "eq", "right": "vendor Corner"}, "next": {"true": "end", "false": "bad"}},
		"bad": {"activity": "fail", "inputs": {"message": "unexpected answer"}}, "end": {"activity": "end"}}}}`)
	item := f.item(t, "alice", "Receipt", nil).ID
	for range 2 {
		ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item}})
		if err != nil {
			t.Fatal(err)
		}
		if run := f.wait(t, ids[0], finished); run.Status != "completed" {
			t.Fatalf("run: %+v", run)
		}
	}
	// The second run reused the first one's answer.
	if n := server.Chats.Load(); n != 1 {
		t.Fatalf("%d chat requests for two equal runs", n)
	}
}
