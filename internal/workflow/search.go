package workflow

import (
	"errors"
	"slices"

	"papergo/internal/dms"
	"papergo/internal/forms"
)

// RoleSearchIndex keeps items' and folders' names, fields, terms, tags and
// permissions in the search store.
const RoleSearchIndex = "search.index"

// searchTermBatch is how many entries search.items_with_term returns.
const searchTermBatch = 500

func init() {
	register(&Activity{Key: "search.sync", Kind: "action",
		Description:  "Brings the search documents of an item or folder to its current state: its surfaces are written, and removed when it is gone. Running it again is harmless.",
		InputSchema:  inputs(`"item_id": {"type": "string", "title": "Item or folder", "description": "Defaults to the run's item, or the resource of the event that started the run."}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"written": {"type": "integer"}, "removed": {"type": "integer"}}}`),
		run:          runSearchSync})
	register(&Activity{Key: "search.items_with_term", Kind: "action",
		Description: "Lists, 500 at a time, the items of the workspace holding the term of the term.changed or term.merged event that started the run, for search.sync in a forEach. Follow next until it is empty.",
		InputSchema: inputs(`"collection_id": {"type": "string", "title": "Collection", "description": "Only this list or library; otherwise every collection without its own search.index workflow."},
			"after": {"type": "string", "title": "After", "description": "The next of the previous page."}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"items": {"type": "array", "items": {"type": "string"}}, "next": {"type": "string"}}}`),
		run:          runSearchItemsWithTerm})

	RegisterRole(&Role{Key: RoleSearchIndex, Name: "Search index",
		Description: "Keeps the names, fields, terms, tags and permissions of items and folders searchable. Turned off for a collection, searches inside it answer search_off, and global search keeps its entries as last indexed, still trimmed by current permissions."})
	RegisterBuiltIn(&BuiltIn{Key: "search.index", Name: "Index for search", Role: RoleSearchIndex, Default: true, System: true,
		Description:      "Keeps items and folders searchable: on every change, permission change and term relabel, re-reads the entry and writes it to the search index.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`),
		Definition: `{
			"triggers": [
				{"type": "item.created", "collection_id": "{param:collection}"}, {"type": "item.updated", "collection_id": "{param:collection}"},
				{"type": "item.published", "collection_id": "{param:collection}"}, {"type": "item.unpublished", "collection_id": "{param:collection}"},
				{"type": "item.deleted", "collection_id": "{param:collection}"}, {"type": "item.restored", "collection_id": "{param:collection}"},
				{"type": "item.purged", "collection_id": "{param:collection}"},
				{"type": "folder.created", "collection_id": "{param:collection}"}, {"type": "folder.updated", "collection_id": "{param:collection}"},
				{"type": "folder.deleted", "collection_id": "{param:collection}"}, {"type": "folder.restored", "collection_id": "{param:collection}"},
				{"type": "folder.purged", "collection_id": "{param:collection}"},
				{"type": "permissions.changed", "collection_id": "{param:collection}", "actions": ["replace", "move"]},
				{"type": "term.changed"}, {"type": "term.merged"},
				{"type": "manual", "collection_id": "{param:collection}", "selection": "per_item"}
			],
			"concurrency": "coalesce",
			"flow": {"start": "route", "nodes": {
				"route": {"activity": "if", "inputs": {"left": "{trigger:type}", "op": "contains", "right": "term."}, "next": {"true": "page", "false": "sync"}},
				"sync": {"activity": "search.sync"},
				"page": {"activity": "search.items_with_term", "inputs": {"collection_id": "{param:collection}", "after": "{step:page.next}"}, "next": {"done": "each"}},
				"each": {"activity": "forEach", "inputs": {"items": "{step:page.items}", "as": "entry"}, "next": {"body": "one", "done": "more"}},
				"one": {"activity": "search.sync", "inputs": {"item_id": "{var:entry}"}},
				"more": {"activity": "if", "inputs": {"left": "{step:page.next}", "op": "not_empty"}, "next": {"true": "page", "false": "end"}},
				"end": {"activity": "end"}
			}}
		}`,
	})
}

func runSearchSync(e *env, in map[string]any) (outcome, error) {
	id := e.itemID
	if v, ok := in["item_id"]; ok {
		var err error
		if id, err = e.scope.text(v); err != nil {
			return outcome{}, err
		}
	} else if id == "" {
		id, _ = e.scope.trigger["resource_id"].(string)
	}
	if id == "" {
		return outcome{}, errors.New("search.sync needs an item or folder")
	}
	written, removed, err := e.t.SyncSearch(e.ctx, []string{id})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"written": written, "removed": removed}}, nil
}

func runSearchItemsWithTerm(e *env, in map[string]any) (outcome, error) {
	collection, err := e.scope.text(in["collection_id"])
	if err != nil {
		return outcome{}, err
	}
	after, err := e.scope.text(in["after"])
	if err != nil {
		return outcome{}, err
	}
	data, _ := e.scope.trigger["data"].(map[string]any)
	var terms []string
	switch e.scope.trigger["type"] {
	case dms.EventTermChanged:
		changed, _ := data["changed_fields"].([]any)
		if slices.ContainsFunc(changed, func(f any) bool { return f == "name" || f == "labels" || f == "synonyms" }) {
			if id, ok := data["term_id"].(string); ok {
				terms = append(terms, id)
			}
		}
	case dms.EventTermMerged:
		for _, key := range []string{"source_term_id", "target_term_id"} {
			if id, ok := data[key].(string); ok {
				terms = append(terms, id)
			}
		}
	}
	// Retained values keep merged source IDs. Relabels and further merges
	// must refresh those entries too, because their words come from the target.
	var aliases []string
	for _, id := range terms {
		expanded, err := e.t.SearchTermAliases(e.ctx, id)
		if err != nil {
			return outcome{}, err
		}
		for _, alias := range expanded {
			if !slices.Contains(aliases, alias) {
				aliases = append(aliases, alias)
			}
		}
	}
	var skip []string
	if collection == "" {
		// Collections with their own search.index workflow index themselves.
		if skip, err = OverriddenCollections(e.ctx, e.t, e.workspaceID, RoleSearchIndex); err != nil {
			return outcome{}, err
		}
	}
	items, err := e.t.EntriesWithTerms(e.ctx, e.workspaceID, collection, skip, aliases, after, searchTermBatch)
	if err != nil {
		return outcome{}, err
	}
	next := ""
	if len(items) == searchTermBatch {
		next = items[len(items)-1]
	}
	if items == nil {
		items = []string{}
	}
	return outcome{Output: map[string]any{"items": items, "next": next}}, nil
}
