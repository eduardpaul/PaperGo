package builtin

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Schema fragments the tools share. Argument schemas reject unknown
// properties at every level a tool defines.

const uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

func idProperty(description string) string {
	return fmt.Sprintf(`{"type": "string", "pattern": %q, "description": %q}`, uuidPattern, description)
}

func limitProperty(max, fallback int) string {
	return fmt.Sprintf(`{"type": "integer", "minimum": 1, "maximum": %d, "default": %d, "description": "Entries per page, 1 to %d."}`, max, fallback, max)
}

var cursorProperty = fmt.Sprintf(`{"type": "string", "minLength": 1, "maxLength": %d, "description": "The next_cursor of the previous page. Repeat every other argument unchanged; omit for the first page."}`, maxCursorBytes)

const surfaceProperty = `{"type": "string", "enum": ["auto", "head", "published"], "default": "auto",
	"description": "Which revision to read: head is the latest draft, published the last published revision, and auto the head where the caller may read drafts and the published revision elsewhere."}`

const keywordsProperty = `{"type": "array", "maxItems": 50, "uniqueItems": true, "items": {"type": "string", "minLength": 1, "maxLength": 255},
	"description": "Shared keywords: existing keyword term IDs or labels; a new label creates the keyword. Replaces every keyword the entry had."}`

const valuesProperty = `{"type": "object", "description": "Custom field values by field key, as get_collection_schema describes them. Numbers keep every digit; decimals may be JSON numbers or strings."}`

// object is an argument schema: the properties in order, the required ones
// and no others.
func object(required []string, properties ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	return json.RawMessage(`{"type": "object", "additionalProperties": false, "required": ` + string(req) +
		`, "properties": {` + strings.Join(properties, ", ") + `}}`)
}

func property(name, schema string) string { return fmt.Sprintf("%q: %s", name, schema) }

// Result schemas.

const nullableString = `{"type": ["string", "null"]}`

var itemSchema = `{"type": "object", "required": ["id", "kind", "workspace_id", "collection_id", "parent_id", "name", "content_type_id", "version", "created_at", "created_by", "updated_at", "updated_by", "keywords", "values"], "properties": {
	"id": {"type": "string"}, "kind": {"type": "string", "enum": ["item", "folder"]},
	"workspace_id": {"type": "string"}, "collection_id": ` + nullableString + `, "parent_id": ` + nullableString + `,
	"name": {"type": "string"}, "content_type_id": ` + nullableString + `,
	"version": {"type": "integer", "description": "The concurrency version update_item requires."},
	"created_at": {"type": "string"}, "created_by": {"type": "string"}, "updated_at": {"type": "string"}, "updated_by": {"type": "string"},
	"keywords": {"type": "array", "items": {"type": "string"}, "description": "Keyword term IDs."},
	"values": {"type": "object", "description": "Custom field values by field key."},
	"access": {"type": "array", "items": {"type": "string", "enum": ["read", "read_draft", "write", "publish", "manage"]}, "description": "What the caller may do with the entry."}}}`

var collectionSchema = `{"type": "object", "required": ["id", "kind", "workspace_id", "name", "description", "template_keys", "publishing_enabled", "allow_folders", "version", "created_at", "updated_at"], "properties": {
	"id": {"type": "string"}, "kind": {"type": "string", "enum": ["list", "library"]}, "workspace_id": {"type": "string"},
	"name": {"type": "string"}, "description": {"type": "string"}, "template_keys": {"type": "array", "items": {"type": "string"}},
	"publishing_enabled": {"type": "boolean", "description": "Item changes are drafts until published."},
	"allow_folders": {"type": "boolean"}, "version": {"type": "integer"}, "created_at": {"type": "string"}, "updated_at": {"type": "string"}}}`

var workspaceSchema = `{"type": "object", "required": ["id", "name", "description", "keywords", "version", "created_at", "updated_at"], "properties": {
	"id": {"type": "string"}, "name": {"type": "string"}, "description": {"type": "string"},
	"keywords": {"type": "array", "items": {"type": "string"}}, "version": {"type": "integer"}, "created_at": {"type": "string"}, "updated_at": {"type": "string"}}}`

// pageSchema is a page of entries, with any further properties.
func pageSchema(entry string, more ...string) json.RawMessage {
	props := append([]string{property("value", `{"type": "array", "items": `+entry+`}`),
		property("next_cursor", `{"type": ["string", "null"], "description": "Pass it as cursor for the next page; null on the last page."}`)}, more...)
	return json.RawMessage(`{"type": "object", "required": ["value", "next_cursor"], "properties": {` + strings.Join(props, ", ") + `}}`)
}
