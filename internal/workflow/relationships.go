package workflow

import (
	"errors"

	"papergo/internal/dms"
	"papergo/internal/forms"
)

// Relationship actions use the same DMS operations and permissions as the API.
// A list page is small enough to walk with forEach, and next_cursor asks for
// the following page.

func init() {
	register(&Activity{Key: "relationship.list", Kind: "action",
		Description: "Lists one page of an item's relationships. direction is both, outgoing or incoming. type limits the list to one relationship type. Pass next_cursor back as cursor for the next page.",
		InputSchema: inputs(itemIDProp + `,
			"direction": {"type": "string", "title": "Direction", "enum": ["both", "outgoing", "incoming"], "default": "both"},
			"type": {"type": "string", "title": "Relationship type"},
			"cursor": {"type": "string", "title": "Cursor", "description": "The next_cursor of the previous page."},
			"limit": {"type": "integer", "title": "Page size", "minimum": 1, "maximum": 100, "default": 50}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"relationships": {"type": "array", "items": {"type": "object", "properties": {
				"id": {"type": "string"}, "source_id": {"type": "string"}, "target_id": {"type": "string"},
				"type_id": {"type": "string"}, "directed": {"type": "boolean"}, "related_id": {"type": "string"}}}},
			"next_cursor": {"type": "string"}, "count": {"type": "integer"}}}`),
		run: runRelationshipList})
	register(&Activity{Key: "relationship.create", Kind: "action",
		Description: "Links the item to target_id. An existing relationship of the same type and direction is kept, so a retry does not create a second one. type is the relationship type's name. directed is taken from the type when omitted.",
		InputSchema: inputs(itemIDProp+`,
			"target_id": {"type": "string", "title": "Target item", "x-papergo": {"kind": "item"}},
			"type": {"type": "string", "title": "Relationship type"},
			"directed": {"type": "boolean", "title": "Directed"}`, "target_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"relationship_id": {"type": "string"}, "source_id": {"type": "string"}, "target_id": {"type": "string"}}}`),
		run: runRelationshipCreate})
	register(&Activity{Key: "relationship.remove", Kind: "action",
		Description:  "Removes one relationship of the item. A relationship that is already gone is reported as removed, so a retry is safe.",
		InputSchema:  inputs(itemIDProp+`, "relationship_id": {"type": "string", "title": "Relationship"}`, "relationship_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"relationship_id": {"type": "string"}, "removed": {"type": "boolean"}}}`),
		run:          runRelationshipRemove})
}

func runRelationshipList(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	direction := "both"
	if err = e.choice(in, "direction", &direction, []string{"both", "outgoing", "incoming"}); err != nil {
		return outcome{}, err
	}
	typeName, err := e.optionalText(in, "type")
	if err != nil {
		return outcome{}, err
	}
	cursor, err := e.optionalText(in, "cursor")
	if err != nil {
		return outcome{}, err
	}
	limit := 50
	if err = e.whole(in, "limit", &limit, 1, 100); err != nil {
		return outcome{}, err
	}
	page, err := e.t.Relationships(e.ctx, e.author, itemID, direction, typeName, cursor, limit)
	if err != nil {
		return outcome{}, err
	}
	rows := make([]any, 0, len(page.Data))
	for _, view := range page.Data {
		related := ""
		if view.RelatedItem.Resource != nil {
			related = view.RelatedItem.ID
		}
		rows = append(rows, map[string]any{
			"id": view.ID, "source_id": view.SourceID, "target_id": view.TargetID,
			"type_id": view.TypeID, "directed": view.Directed, "related_id": related,
		})
	}
	return outcome{Output: map[string]any{"relationships": rows, "next_cursor": page.NextCursor, "count": len(rows)}}, nil
}

func runRelationshipCreate(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	targetID, err := e.scope.text(in["target_id"])
	if err != nil {
		return outcome{}, err
	}
	if targetID == "" {
		return outcome{}, errors.New("target_id is empty")
	}
	typeName, err := e.optionalText(in, "type")
	if err != nil {
		return outcome{}, err
	}
	link := dms.CreateRelationship{Type: typeName, TargetID: targetID}
	if raw, ok := in["directed"]; ok {
		value, err := e.scope.resolve(raw)
		if err != nil {
			return outcome{}, err
		}
		directed, ok := value.(bool)
		if !ok {
			return outcome{}, errors.New("directed must be true or false")
		}
		link.Directed = &directed
	}
	created, err := e.t.Link(e.ctx, e.author, itemID, link)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"relationship_id": created.ID, "source_id": created.SourceID, "target_id": created.TargetID}}, nil
}

func runRelationshipRemove(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	id, err := e.scope.text(in["relationship_id"])
	if err != nil {
		return outcome{}, err
	}
	if id == "" {
		return outcome{}, errors.New("relationship_id is empty")
	}
	if err = e.t.Unlink(e.ctx, e.author, itemID, id); err != nil {
		if errors.Is(err, dms.ErrNotFound) {
			return outcome{Output: map[string]any{"relationship_id": id, "removed": false}}, nil
		}
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"relationship_id": id, "removed": true}}, nil
}
