# Workspace Relationships explorer

## Product direction

Provide a performant, read-only exploration surface beside Keywords at
`/w/$workspaceId/relationships`. The primary journey is:
**choose a relationship type → find a connection → explore an item → follow
another connection**.

The list is the default. An optional graph shows a selected item's immediate
neighborhood. Every displayed relationship must touch the starting workspace,
including when an external item becomes the focus. Design for 100,000 items and
1 million relationships, including individual items with 50,000 connections.

Relationship creation, editing and removal remain in existing item screens.
Whole-workspace graphs, automatic multi-hop expansion, document-body search,
attribute-filter builders and exact global counts are outside this version.

## Screen and interactions

```text
Workspace / Relationships

[Find an item to explore…]     [Collection] [Scope]

Relationship types       Connections                         Inspector
[Filter types…]          [Workspace connections > Item A]    Item or relationship
All relationships        [List | Graph]                      details
References               Source — Relationship → Target
Depends on               Source — Relationship — Target     [Explore item]
Related to                                                  [Open item]
Untyped                  [Load more]
```

- Use PaperGo's existing typography, neutral surfaces, accent and light/dark
  themes. Give connections most of the available width.
- Page the searchable type navigation over types with authorized workspace
  connections. Include All relationships and Untyped, without exact usage counts.
  Select All relationships initially and immediately load a page of connections.
- Search readable endpoint names, including external items. The current picker
  searches from the beginning of names and supports loading more suggestions.
- Filter by a list/library in the starting workspace and by All, Within this
  workspace or Across workspaces. Collection filtering requires an endpoint in
  the chosen collection.
- Present each row as source, relationship and target with collection locations;
  external endpoints also show their workspace. Preserve separate edges between
  the same items. Symmetric links have no directional arrow.
- Selecting an endpoint focuses it in the explorer. Selecting a relationship
  opens its details, attributes, creation time and endpoint actions. Open item
  navigates to the existing item screen as a separate action.
- Focused exploration retains filters and adds All, Incoming, Outgoing and
  Symmetric direction choices. Use inverse labels for incoming links when
  defined; otherwise retain the canonical label and indicate direction.
- Keep a navigation trail, Back and Workspace connections reset. Returning
  restores filters, selection and list position. Empty filtered neighborhoods
  offer Show all relationship types and Clear filters where applicable.
- When focused on an external item, state "Showing connections to [workspace]".
  Its other relationships wholly outside the starting workspace remain excluded.
- The graph requires an item focus, contains at most 25 nodes and 50 edges, and
  groups parallel connections with individually selectable relationships.
  Incoming, outgoing and symmetric connections have distinct layout regions.
  Explore from here replaces the neighborhood and records a navigation step.
  Truncated graphs explain the limit and link to the list; switching views
  preserves focus and filters.
- Target a type drawer below 1280px and stacked rows with a full-width detail
  sheet below 768px. Keep every graph action available through the list. Support
  keyboard navigation, visible focus, screen-reader direction labels, full-name
  disclosure and reduced motion.
- Distinguish no relationships, no filter matches, unavailable items, and request
  failures. Retry only the affected region.

## Data and performance

Use typed repository methods in `internal/dal/content`, its engine-specific
access expressions, and the existing typed relationship filter compiler.
Selected surfaces and both endpoint permissions apply before paging or discovery.
List reads return compact endpoint summaries and relationship labels in one query;
full attributes and policy schemas load on inspection.

| Read endpoint | Purpose |
| --- | --- |
| `GET /v1/workspaces/{id}/relationships` | Cursor-paged workspace connections, optionally focused on an item |
| `GET /v1/workspaces/{id}/relationships/types` | Paginated used-type discovery and label search |
| `GET /v1/workspaces/{id}/relationships/items` | Paginated endpoint-name suggestions or a located focused endpoint |
| `GET /v1/workspaces/{id}/relationships/neighborhood` | Bounded immediate graph and a truncation flag |

The connection query supports type/untyped, focused item, collection, workspace
scope, direction, directed flag, scalar relationship filters and cursor paging.
IDs provide stable connection ordering; discovery pages sort by name and ID.
Indexes in both DAL schema masters cover type/ID and folded surface names.
Item surfaces store `folded_name`, the name lowered for every script as Go
does; endpoint search, ordering and cursors use it, so names starting with
any letter are found on both engines. Type search matches the folded name,
label values and synonyms, not label languages.

Frontend defaults are 50 rows per page, explicit Load more, virtualized rows,
250ms search debounce, cancellation of superseded requests, and at most 20 cached
pages per query. Query caches expire after inactivity. The graph loads lazily and
fetches its neighborhood only when selected. Do not fetch whole relationship sets
or block results on exact totals; show the number of loaded connections.

Store type, item focus, collection, scope, direction, view, inspected relationship
and a bounded trail in route search state. Restore scrolling within the session.

## Checkpoint and remaining validation

Implemented in this checkpoint:

- Workspace entry point, explorer route, type navigation, endpoint picker,
  filters, virtualized connection list, inspection and bounded graph.
- Typed service queries, compact DAL reads, relationship discovery endpoints,
  selected-surface authorization, paired indexes and updated existing callers.
- Integration with the current DAL resource, content and access packages.

Automated coverage on both engines: connection filters (type, untyped, scope,
direction, collection), external focus anchoring, cursor paging, type discovery
by name, label and synonym, endpoint search across scripts with paging, and the
bounded neighborhood. The app has not been exercised by hand; this is an
implementation checkpoint, not a completion claim.

Remaining acceptance work:

- Exercise typed/untyped, directed/symmetric, inverse-labelled, parallel and
  cross-workspace connections; verify external pivots stay workspace anchored.
- Test fragmented ACLs, credential scopes, draft/published names, deletions,
  permission changes and absence of leaks through discovery or graph data.
- Verify cursor stability, filter resets, request cancellation, deep links,
  browser Back, scroll restoration and cache eviction behavior.
- Review keyboard and screen-reader use, mobile layouts, both themes, long
  labels, empty/error states, graph edge selection and visible navigation trail.
- Benchmark SQLite and PostgreSQL with 100k items / 1m links, a 50k-link hub,
  selective filters and restricted readers. Record the environment and query
  plans. The target is p95 at most 500ms for bounded reads under eight concurrent
  readers on a documented 4-vCPU / 8-GB reference environment, and usable results
  within one second of filter submission. These targets are not measured results.
- Complete visual/runtime QA before marking the feature complete. Confirm graph
  limits through repeated exploration.
