package runner

import (
	"net/http"
	"slices"
	"testing"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/workflow"
)

// With an embedding model (a fake OpenAI-compatible server), search.content
// embeds file chunks before publishing them: search defaults to hybrid and
// finds files by meaning, with the page. When the provider fails, content is
// published without vectors and searches fall back to keyword; the sweep
// embeds the rest once the provider is back.
func TestSearchContentEmbedsForSemanticSearch(t *testing.T) {
	doctexttest.PDF(t)
	server := aitest.New(t)
	model, err := ai.NewEmbedder(ai.EmbeddingsConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "fake-embed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, func(tools *workflow.Tools) { tools.Embeddings = model })
	f.organizationManager(t, "alice")
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Bills", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	bill := f.upload(t, lib.ID, "utility.pdf", "application/pdf", doctexttest.TextPDF("Welcome", "The invoice payment is overdue"))
	f.waitContent(t, bill.ID, bill.Blob.ID)
	status, err := f.dms.SearchStatus(ctx, "alice")
	if err != nil || status.Model != "openai:fake-embed:64" || status.Embeddings == nil || status.Embeddings.Pending != 0 || status.Embeddings.Vectors != 2 || status.Embeddings.MemoryBytes != 2*64*4 {
		t.Fatalf("status: %+v %+v %v", status, status.Embeddings, err)
	}

	f.searchNames(t, "alice", dms.SearchRequest{Q: "overdue", Mode: "keyword", Surface: "head"}, "utility.pdf")
	// Not every word is in the file: keyword finds nothing, meaning does.
	keyword, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "invoice payment overdue penalty", Mode: "keyword", Surface: "head"})
	if err != nil || len(keyword.Hits) != 0 {
		t.Fatalf("keyword: %+v %v", keyword.Hits, err)
	}
	found, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "invoice payment overdue penalty", Surface: "head"})
	if err != nil || found.Mode != "hybrid" || len(found.Hits) != 1 {
		t.Fatalf("hybrid: %+v %v", found, err)
	}
	if h := found.Hits[0]; h.ID != bill.ID || h.Page != 2 || !slices.Equal(h.MatchedBy, []string{"semantic"}) || h.Snippet == "" {
		t.Fatalf("semantic hit: %+v", h)
	}
	both, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "invoice overdue", Mode: "semantic", Surface: "head"})
	if err != nil || both.Mode != "semantic" || len(both.Hits) != 1 || !slices.Equal(both.Hits[0].MatchedBy, []string{"semantic"}) {
		t.Fatalf("semantic: %+v %v", both, err)
	}
	// Someone outside the workspace finds nothing.
	if other, err := f.dms.Search(ctx, "carol", dms.SearchRequest{Q: "invoice payment overdue penalty", Surface: "head"}); err == nil && len(other.Hits) != 0 {
		t.Fatalf("an outsider found %+v", other.Hits)
	}

	// The provider fails: the content is published without vectors.
	server.Fail(1_000_000, http.StatusServiceUnavailable)
	late := f.upload(t, lib.ID, "water.pdf", "application/pdf", doctexttest.TextPDF("Water bill reminder for the overdue amount"))
	f.waitContent(t, late.ID, late.Blob.ID)
	f.searchNames(t, "alice", dms.SearchRequest{Q: "reminder", Mode: "keyword", Surface: "head"}, "water.pdf")
	content := f.builtIn(t, "search.content", lib.ID)
	runs := f.runs(t, content.ID, 2)
	for _, run := range runs {
		if run.Status != "completed" {
			t.Fatalf("a provider failure failed the run: %+v", run)
		}
	}
	if status, err = f.dms.SearchStatus(ctx, "alice"); err != nil || status.Embeddings.Pending != 1 {
		t.Fatalf("pending after a failure: %+v %v", status.Embeddings, err)
	}
	fallback, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "water reminder", Surface: "head"})
	if err != nil || fallback.Mode != "keyword" || len(fallback.Hits) != 1 || fallback.Hits[0].ID != late.ID {
		t.Fatalf("keyword fallback: %+v %v", fallback, err)
	}
	if n, err := f.wf.SweepEmbeddings(ctx); err == nil || n != 0 {
		t.Fatalf("sweep while the provider fails: %d %v", n, err)
	}

	// The provider is back: the sweep embeds what is left.
	server.Fail(0, 0)
	if n, err := f.wf.SweepEmbeddings(ctx); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if status, err = f.dms.SearchStatus(ctx, "alice"); err != nil || status.Embeddings.Pending != 0 || status.Embeddings.Vectors != 3 {
		t.Fatalf("after the sweep: %+v %v", status.Embeddings, err)
	}
	semantic, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "water bill reminder overdue amount please", Mode: "semantic", Surface: "head"})
	if err != nil || len(semantic.Hits) == 0 || semantic.Hits[0].ID != late.ID {
		t.Fatalf("swept content: %+v %v", semantic.Hits, err)
	}
}

// search.embed with mode later defers to the sweep; without a model it does
// nothing; semantic search without a model is refused.
func TestSearchEmbedLaterAndWithoutModel(t *testing.T) {
	doctexttest.PDF(t)
	f := setup(t)
	if _, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "invoice", Mode: "hybrid"}); !isValidation(err) {
		t.Fatalf("hybrid without a model: %v", err)
	}
	if found, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "invoice"}); err != nil || found.Mode != "keyword" {
		t.Fatalf("default mode without a model: %+v %v", found, err)
	}
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Bills", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "search.content", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true, Parameters: map[string]any{"embed": "later"}}); err != nil {
		t.Fatal(err)
	}
	file := f.upload(t, lib.ID, "bill.pdf", "application/pdf", doctexttest.TextPDF("Gas bill"))
	f.waitContent(t, file.ID, file.Blob.ID)
	run := f.runs(t, f.builtIn(t, "search.content", lib.ID).ID, 1)[0]
	if run.Status != "completed" {
		t.Fatalf("run: %+v", run)
	}
	if n, err := f.wf.SweepEmbeddings(ctx); err != nil || n != 0 {
		t.Fatalf("sweep without a model: %d %v", n, err)
	}
}

func isValidation(err error) bool {
	_, ok := err.(*dms.ValidationError)
	return ok
}
