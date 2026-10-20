package dms

import (
	"slices"
	"testing"
)

// explorerFixture links items of a workspace's two lists and one item of
// another workspace:
//
//	Ärzteverband -Supplies-> Apfel
//	Ärzteverband --(untyped)-- Zug
//	Zäune (second list) -Supplies-> Ärzteverband
//	Ärzteverband --(untyped)-- Remote (other workspace)
type explorerFixture struct {
	s                                    *Service
	workspace, otherList, supplies       string
	doctors, apple, train, fence, remote string
}

func newExplorerFixture(t *testing.T) explorerFixture {
	t.Helper()
	s, w, list := fixture(t)
	other := create(t, s, w.ID, "list", "Second list", nil)
	away := create(t, s, "", "workspace", "Away", nil)
	awayList := create(t, s, away.ID, "list", "Away list", nil)
	f := explorerFixture{s: s, workspace: w.ID, otherList: other.ID,
		doctors: create(t, s, list.ID, "item", "Ärzteverband", nil).ID,
		apple:   create(t, s, list.ID, "item", "Apfel", nil).ID,
		train:   create(t, s, list.ID, "item", "Zug", nil).ID,
		fence:   create(t, s, other.ID, "item", "Zäune", nil).ID,
		remote:  create(t, s, awayList.ID, "item", "Remote", nil).ID,
	}
	typ, err := s.CreateRelationshipType(testContext, "alice", RelationshipTypeInput{Name: "Supplies", InverseLabel: "Supplied by", Directed: true})
	if err != nil {
		t.Fatal(err)
	}
	f.supplies = typ.ID
	term, err := s.Term(testContext, "alice", typ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTerm(testContext, "alice", term.ID, term.Version, TermInput{Name: "Supplies", Labels: map[string]string{"de": "Liefert"}, Synonyms: []string{"Furnishes"}}); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct {
		from string
		in   CreateRelationship
	}{
		{f.doctors, CreateRelationship{Type: typ.ID, TargetID: f.apple}},
		{f.doctors, CreateRelationship{TargetID: f.train}},
		{f.fence, CreateRelationship{Type: typ.ID, TargetID: f.doctors}},
		{f.doctors, CreateRelationship{TargetID: f.remote}},
	} {
		if _, err = s.Link(testContext, "alice", link.from, link.in); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// peers lists the endpoints of the connections other than item, sorted.
func (f explorerFixture) peers(t *testing.T, item string, in WorkspaceRelationshipsQuery) []string {
	t.Helper()
	in.ItemID = item
	page, err := f.s.WorkspaceRelationships(testContext, "alice", f.workspace, in)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, edge := range page.Data {
		peer := edge.SourceItem.Name
		if edge.SourceID == item {
			peer = edge.TargetItem.Name
		}
		out = append(out, peer)
	}
	slices.Sort(out)
	return out
}

func TestRelationshipExplorerFilters(t *testing.T) {
	f := newExplorerFixture(t)
	for name, test := range map[string]struct {
		in   WorkspaceRelationshipsQuery
		want []string
	}{
		"all":        {WorkspaceRelationshipsQuery{}, []string{"Apfel", "Remote", "Zug", "Zäune"}},
		"typed":      {WorkspaceRelationshipsQuery{Type: f.supplies}, []string{"Apfel", "Zäune"}},
		"untyped":    {WorkspaceRelationshipsQuery{Untyped: true}, []string{"Remote", "Zug"}},
		"internal":   {WorkspaceRelationshipsQuery{Scope: "internal"}, []string{"Apfel", "Zug", "Zäune"}},
		"external":   {WorkspaceRelationshipsQuery{Scope: "external"}, []string{"Remote"}},
		"outgoing":   {WorkspaceRelationshipsQuery{Direction: "outgoing"}, []string{"Apfel"}},
		"incoming":   {WorkspaceRelationshipsQuery{Direction: "incoming"}, []string{"Zäune"}},
		"symmetric":  {WorkspaceRelationshipsQuery{Direction: "symmetric"}, []string{"Remote", "Zug"}},
		"collection": {WorkspaceRelationshipsQuery{CollectionID: f.otherList}, []string{"Zäune"}},
	} {
		if got := f.peers(t, f.doctors, test.in); !slices.Equal(got, test.want) {
			t.Errorf("%s: %v, want %v", name, got, test.want)
		}
	}
	// An external focus stays anchored to the starting workspace.
	if got := f.peers(t, f.remote, WorkspaceRelationshipsQuery{}); !slices.Equal(got, []string{"Ärzteverband"}) {
		t.Errorf("external focus: %v", got)
	}
	// Pages of one connection visit every connection once.
	seen := map[string]bool{}
	in := WorkspaceRelationshipsQuery{Limit: 1}
	for range 10 {
		page, err := f.s.WorkspaceRelationships(testContext, "alice", f.workspace, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range page.Data {
			if seen[edge.ID] {
				t.Fatal("connection repeated across pages", edge.ID)
			}
			seen[edge.ID] = true
		}
		if in.After = page.NextCursor; in.After == "" {
			break
		}
	}
	if len(seen) != 4 {
		t.Fatalf("paged %d connections, want 4", len(seen))
	}
	if _, err := f.s.WorkspaceRelationships(testContext, "alice", f.workspace, WorkspaceRelationshipsQuery{Direction: "incoming"}); err == nil {
		t.Fatal("incoming without a focus accepted")
	}
}

func TestRelationshipExplorerDiscovery(t *testing.T) {
	f := newExplorerFixture(t)
	types := func(search string) []string {
		page, err := f.s.WorkspaceRelationshipTypes(testContext, "alice", f.workspace, WorkspaceRelationshipsQuery{Search: search})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, typ := range page.Data {
			out = append(out, typ.Name)
		}
		return out
	}
	// The name, a localized label and a synonym match; a label's language does not.
	for search, want := range map[string][]string{"": {"Supplies"}, "suppl": {"Supplies"}, "LIEF": {"Supplies"}, "furnish": {"Supplies"}, "de": {}} {
		if got := types(search); !slices.Equal(got, want) {
			t.Errorf("types %q: %v, want %v", search, got, want)
		}
	}
	items := func(search string, limit int) []string {
		out := []string{}
		in := WorkspaceRelationshipsQuery{Search: search, Limit: limit}
		for range 10 {
			page, err := f.s.WorkspaceRelationshipItems(testContext, "alice", f.workspace, in)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Data {
				out = append(out, item.Name)
			}
			if in.After = page.NextCursor; in.After == "" {
				break
			}
		}
		return out
	}
	// Names fold for every script, and pages resume by the folded name. The
	// engines' collations may order folded names differently.
	for search, want := range map[string][]string{"ärz": {"Ärzteverband"}, "ÄRZ": {"Ärzteverband"}, "z": {"Zug", "Zäune"}, "a": {"Apfel"}, "rem": {"Remote"}} {
		got := items(search, 1)
		slices.Sort(got)
		if slices.Sort(want); !slices.Equal(got, want) {
			t.Errorf("items %q: %v, want %v", search, got, want)
		}
	}
	graph, err := f.s.WorkspaceRelationshipNeighborhood(testContext, "alice", f.workspace, WorkspaceRelationshipsQuery{ItemID: f.doctors})
	if err != nil || len(graph.Nodes) != 5 || len(graph.Edges) != 4 || graph.Truncated {
		t.Fatal(graph, err)
	}
}
