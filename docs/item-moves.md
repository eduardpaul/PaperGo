# Item moves

`POST /v1/items/{itemID}/move` accepts `collection_id` and an optional `parent_id`. Empty or omitted `parent_id` selects the destination root. Supply the item's current ETag in `If-Match`.

Both ends require write access; the destination collection must also be readable. Cross-collection moves support content items, including moves across workspaces, when the collection kinds match and the destination has the item's content type attached. All current values, references and rules are validated against the destination. Folder trees move within their collection only.

A cross-collection move retains the item ID, creation metadata, file bytes, relationships, revision history and explicit publication snapshot. Historical revisions keep their original container and frozen schema as provenance. Search projections, indexed field values, business-key claims and workflow run workspace metadata follow the item. The item inherits destination permissions and loses its exclusive source grants. Relationships are visible only to callers who can read both live endpoints.

A destination with automatic publishing exposes the item's head. Explicit publishing retains the current publication. A move alone creates no content revision; normalization that changes values creates a new revision. Moves emit `item.updated` in the destination and record `resource.move` audit details with the old and new locations.

Destination name collisions, unique-value conflicts, invalid references, stale ETags and failed writes roll back the whole move. Existing `PATCH /v1/resources/{id}` parent moves remain within the same collection and retain exclusive item/folder permissions.
