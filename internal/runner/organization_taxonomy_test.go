package runner

import (
	"encoding/json"
	"papergo/internal/dms"
	"papergo/internal/workflow"
	"testing"
)

func TestSharedTaxonomyMergeDispatchesToBothWorkspaces(t *testing.T) {
	f := setup(t)
	otherWorkspace, err := f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	definition := `{"triggers":[{"type":"term.merged"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`
	first := f.workflow(t, "Curation notification", definition)
	second, err := f.wf.Create(ctx, "alice", otherWorkspace.ID, workflow.Save{Name: "Curation notification", Definition: json.RawMessage(definition)})
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Corporate"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Accounting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.MergeTerm(ctx, "alice", source.ID, source.Version, dms.MergeTermInput{TargetTermID: target.ID}); err != nil {
		t.Fatal(err)
	}
	if runs := f.runs(t, first.ID, 1); runs[0].Status != "completed" {
		t.Fatal(runs)
	}
	secondScope := *f
	secondScope.ws = otherWorkspace
	if runs := secondScope.runs(t, second.ID, 1); runs[0].Status != "completed" {
		t.Fatal(runs)
	}
}
