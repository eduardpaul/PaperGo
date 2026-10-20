# MCP server port from PaperDotNet

Status: implemented in `cmd/mcp` and `extensions/mcp`; see [MCP](../mcp.md).
Reviewed against PaperGo `main` at
`15733bcb4a062359ecdfea9d8a922ca521a77f6c` on 2026-10-20, and again at
`a8769d2` before implementation (see [review against a8769d2](#review-against-a8769d2)).

## Review against a8769d2

`main` changed after the first review. The design holds; these points adjust it:

- **Search is optional.** Search is now the `internal/documentsearch` extension, which a
  deployment may run without (`SEARCH_STORE=none`). `GET /v1/search` exists only
  when it runs, so the `search` tool is a contribution-style `Available` check:
  it is listed and callable only when the backend's workflow catalog
  (`GET /v1/workflow-catalog`) holds search's `search.index` role, as the web app
  decides. Without search, `query_items`' `query.search` still works: the backend
  matches names.
- **`ancestor_id`.** `QuerySpec` accepts `ancestor_id`, but `api/openapi.json` did
  not describe it. The contract now does; REST behavior is unchanged.
- **SDK.** go-sdk v1.8.0 negotiates 2026-07-28 down to 2024-11-05, answers GET
  and DELETE with 405 when stateless, and its `ServerOptions.SetCacheable` and
  `StreamableHTTPOptions.PropagateRequestCancellation` exist as the plan assumes.
- **Scopes.** The identity catalog spells scopes as strings; the extension names
  the wire scopes it requires as its own constants.

## Acceptance record

Automated, with the official go-sdk client against the real API handlers, on
SQLite and on PostgreSQL 17 (`extensions/mcp/integration`, also run with
`-race`), plus pure-Go unit tests of the registry, configuration, path safety,
cursors and the executable:

- Tool listing, schemas and annotations; protocol revisions 2026-07-28 to
  2024-11-05 without sessions; GET/DELETE 405; private, uncacheable responses;
  health; logs free of credentials.
- Two users concurrently, scope-narrowed personal tokens, an integration
  token's own identity, revocation, missing/malformed/invalid credentials and
  an inactive user. Hidden and unknown tools fail alike.
- Contribution availability for listing and direct calls; a disabled
  contribution is not served; invalid, duplicate and colliding registrations
  fail startup.
- Workspace and collection discovery, both collection kinds, folders, schema
  reads and a schema change between reads; structured filters, sorts,
  `select`, paging, empty pages, mismatched cursors and hostile next links.
- Integers above 2^53 and scaled decimals in filters, values and results, on
  the wire in both text and structured content.
- Create with a caller ID repeated, wrong parent kinds, invalid values,
  updates with patch/null semantics, stale versions, and a write whose answer
  is lost (sent once, never retried).
- Keyword search with paging when search runs, another user's items absent,
  and the tool absent when it does not.
- Origin and Host protection, overload with `Retry-After`, request and result
  size limits, an unreachable or malformed backend, readiness, and graceful
  shutdown draining a request in flight.

Not automated: OIDC provider tokens, semantic and hybrid search with an
embedding provider, task and event entries, and search-index leakage checks
beyond permissions (tombstones, unpublished drafts, `document.read` on file
text); those paths are the API's own and covered by its suites. The image
build was verified locally; CI builds it in the `MCP server` job.

## 1. Goal and architectural decision

Let an MCP client discover workspaces and collections, inspect schemas, search,
query, read, create and update items with the connecting user's current access.
Use PaperGo terminology, snake_case arguments, structured queries and required
write versions. Include a compiled tool registry and schema discovery. Connect
with existing bearer credentials; prepare a boundary for subsequent OAuth work,
without implementing OAuth discovery or sign-in now.

Apply the [Extension-First Protocol](../../AGENTS.md): **implement MCP as an
optional REST-consuming extension**, with a separate `papergo-mcp` process.
The existing HTTP API supplies all required data and mutations. No generic core
socket is missing for this design. Do not change DMS, identity, HTTP API routing,
base DAL tables, schemas or generated models to implement this feature.

The executable lives at `cmd/mcp`; implementation and the contribution contract
live under `extensions/mcp`. The executable composes built-ins and explicitly
registered compiled contributions. It talks to one configured PaperGo backend.
Deployment can route public `/v1/mcp` to this process while retaining `/v1/*` on
the API. A separate MCP hostname also works. This is an additional executable in
this repository, not a new database owner or a separate repository.

An embedded alternative would require an authenticated HTTP-module mounting
socket and an extensible scope catalog. Both could also benefit calendar,
import/export or other protocol modules, but neither exists on the reviewed
main. They are unnecessary for this port. If embedding later becomes a
requirement, propose those generic sockets and request approval before changing
core, as AGENTS.md requires.

## 2. Source analysis and mapping to current main

Reference checkout: `/home/eduapaul/repos/PaperDotNet-aot`, HEAD
`0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d`. The reviewed MCP files have no local
modifications. The analysis is a source review; PaperDotNet was not built or run.

| PaperDotNet source | Implemented behavior to carry over |
| --- | --- |
| [McpModule.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/src/Modules/Mcp/PaperDotNet.Mcp/McpModule.cs) | Official SDK; stateless Streamable HTTP at `/v1.0/mcp`; per-request scope and availability filtering for both listing and execution; caller identity; annotations; text plus structured results |
| [McpTools.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/src/Modules/Mcp/PaperDotNet.Mcp.Contracts/McpTools.cs) | Protocol-independent tool contract, typed arguments, JSON schemas and recoverable tool failures |
| [ItemTools.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/src/Modules/Mcp/PaperDotNet.Mcp/Features/ItemTools.cs) | Workspace/list discovery and generic item query/get/create/update; query limit 50 by default, maximum 200; optional write version |
| [SearchTool.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/src/Modules/Search/PaperDotNet.Search/Features/SearchTool.cs) | Keyword/semantic/hybrid search; default 10 hits, maximum 50; snippets, matching pages and item locations |
| [ExtensionRuntime.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/Runtime/ExtensionRuntime.cs) | Compiled contributions, normalized extension-name prefixes, availability checks during listing and execution |
| [McpTests.cs](https://github.com/eduardpaul/PaperDotNet/blob/0ff5ebfc43214dbd9f039c6704bf80d9feb2b23d/tests/PaperDotNet.IntegrationTests/McpTests.cs) | Real SDK client round trips, CRUD/search, unauthenticated rejection, tenant isolation and enabled-extension visibility |

PaperDotNet also requires `mcp.use`, uses `search.read`, accepts OData filters,
and resolves a tenant. PaperGo has one organization per deployment, search uses
`list.read`, and query inputs are `QuerySpec`/`FilterExpr` objects. Preserve those
PaperGo rules. Do not introduce tenant headers, OData parsing, camelCase aliases,
PaperDotNet routes or a second item writer.

The reviewed main has completed the Ent-to-Bob conversion: resource DTOs are in
`internal/dal/resources`, query persistence is in `internal/dal/content`, and
schema records are in `internal/dal/catalog`. DMS provides `Browse`, `Query`,
`GetSurface`, `CreateWithStatus`, `Update`, `Search`, `Fields`,
`CollectionContentTypes` and `ItemSchema`. The existing REST routes expose the
necessary operations. The extension consumes their wire contracts rather than
linking the DMS, database drivers, search engine or native processing libraries.
Domain enum definitions may be reused for typed wire values; use their constants
rather than spelling database fixed-value sets as Go string literals.

Important source details that must not become porting mistakes:

- PaperDotNet's actual name check excludes dots despite comments in its
  contract. Use `^[A-Za-z0-9_-]{1,64}$` and fail registration on invalid or
  duplicate names; do not silently discard contributions.
- Generic item tools cover task/event list entries and document metadata. They
  do not implement file upload/download or workflow-task response APIs.
- PaperGo names items with `name`, stores custom data in `values`, patches only
  supplied values, and removes a value on explicit null. Update versions are
  mandatory. Preserve exact integers and decimals across every JSON boundary.
- Reads and search select authorized head/published surfaces. Never substitute
  an unqualified database read or expose a draft to a published-only reader.
- Collection schema history requires manage permission. Schema discovery must
  use fields and attached content types, not the schema-history endpoint.
- PaperGo has compiled workflow activities, not PaperDotNet's installable,
  tenant-gated extension host. Do not build an extension installer for MCP.

## 3. Module, transport and authentication

### Package and runtime boundaries

- Add `cmd/mcp` for flags/environment, server lifecycle, signal handling and
  composition. Add `extensions/mcp` for its public registry/handler contract,
  with implementation packages beneath it for transport, backend access and
  built-ins. Keep SDK types out of the contribution contract.
- Add the official `github.com/modelcontextprotocol/go-sdk` dependency pinned
  to `v1.8.0`. Its [release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)
  supports protocol revision `2026-07-28` and exposes per-request cache policy.
  Delegate framing, negotiation, discovery/initialization and JSON-RPC to the
  SDK. Do not implement a parallel protocol stack or enable compatibility flags.
- Serve stateless Streamable HTTP at exactly `/v1/mcp`, with JSON responses.
  Use `NewStreamableHTTPHandler` and a fresh caller-filtered server per HTTP
  request; share immutable tool definitions/schema compilation only. Preserve
  the SDK's tools capability even when this caller can see zero tools.
- Use SDK-supported protocol negotiation, including its earlier supported
  revisions; add no application compatibility branches. No persistent MCP
  sessions, resumption store, stdio, prompts, resources, subscriptions, sampling
  or server-initiated requests are part of this port.
- Keep the SDK's default localhost protection, wrap the endpoint in Go's
  cross-origin protection and explicitly validate any supplied Origin against
  the configured public origin/allowlist. Native clients without Origin work.
  Do not enable wildcard credentialed CORS. The pinned SDK's
  [transport options](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/streamable.go)
  provide request limits and request-cancellation propagation.

### Existing-credential path

1. Accept one `Authorization: Bearer ...` header on every MCP request. Support
   PaperGo personal/integration tokens and provider/development bearer tokens
   in the modes where the backend accepts them. Recommend personal tokens for
   a user assistant and dedicated integration identities for automation.
2. Resolve the caller through authenticated `GET /v1/identity/me` on the fixed
   backend. Its `scopes` already intersect credential restrictions and current
   role grants. An identity failure stops the request before MCP dispatch.
3. Construct a request-bound backend client using that same credential. Forward
   credentials only to the configured API origin. Do not use an administrator
   credential, token fallback, asserted subject, shared mutable Authorization
   header, browser session cookie, proxy identity headers or client-chosen URL.
4. Filter the request's tool definitions by effective scopes, configured
   enablement and contribution availability. Recheck availability and required
   scopes before execution. Each backend operation independently reauthenticates
   and authorizes the same caller, so changes or revocation during a call fail
   closed rather than trusting the initial discovery result.
5. Return HTTP 401 with a Bearer challenge for absent/invalid credentials;
   propagate backend inactive/forbidden admission as 403. Backend outages are
   503, not an invalid-token response. Never cache principals or token results
   between requests. Unknown and inaccessible tools have the same public error.

Trusted-proxy deployments use integration bearer tokens through an API ingress
that forwards Authorization. Local deployments can issue personal tokens using
their existing login/admin UI. Proxy Basic assertions, local Basic passwords
and browser cookies are not forwarded by this bearer-only MCP endpoint.

Do not add `mcp.use` or `search.read` to the core catalog. Existing domain scopes
govern access, and running/routing the optional MCP module enables the protocol.
This intentionally omits PaperDotNet's separate per-user protocol gate. A future
requirement for such a gate must first propose a generic module-scope
registration socket and obtain approval; do not hard-code MCP into identity.

### Future OAuth boundary; infrastructure only

Define module-local `CredentialResolver.Resolve(ctx, request)` returning the
verified subject, effective scopes and a caller-bound `Backend` interface.
The sole initial implementation performs the bearer relay above. Tools receive
this resolved context, never parse credentials or mint their own clients.
Keep public URL/authorization challenges at the transport boundary so a later
resolver can supply OAuth metadata and challenges without changing tool inputs.

Do not add OAuth endpoints, registration, authorization-code/PKCE handlers,
refresh tokens, unused database tables, inactive OAuth configuration or placeholder
implementations. The later OAuth design must choose the IdP/client registration
policy and validate resource/audience binding. An MCP-audience token cannot simply
be forwarded to an API with a different audience; that design must arrange an
API-valid delegated credential or separately approved shared authorization
socket. Record this next step in the connection documentation, without implying
automatic browser sign-in exists.

## 4. Tool contract and backend mapping

Every argument schema is an object with explicit required fields and unknown
top-level fields rejected. IDs are nonempty UUIDs. Integer arguments are JSON
integers, not numeric strings. Bounds are validated, not silently clamped.
`limit` is 1–200, default 50, except search (1–50, default 10).

| Tool | Arguments | Backend operation and scopes |
| --- | --- | --- |
| `list_workspaces` | Optional `limit`, `cursor` | `GET /v1/workspaces`; `workspace.read` |
| `list_collections` | Required `workspace_id`; optional `limit`, `cursor` | Verify workspace kind with `GET /v1/resources/{id}`, then `GET /v1/resources/{workspace_id}/children`; `workspace.read` plus `list.read`. A workspace's immediate children are lists/libraries. Do not enumerate all workspaces to synthesize an unbounded global list. |
| `get_collection_schema` | Required `collection_id` | Read the resource, `/fields` and `/content-types`; `list.read` plus `contentType.read`. Return current fields, attached content types, their rules/default designation and collection settings. |
| `query_items` | Required `collection_id`; optional `query`, `select`, `surface`, `limit`, `cursor` | `POST /v1/resources/{id}/query`; `list.read`. Forward PaperGo's structured `QuerySpec`, selected fields and surface. No OData or grouping/count API in this tool. |
| `get_item` | Required `item_id`; optional `surface` | `GET /v1/resources/{id}`; `list.read`. Accept item/folder kinds; include the backend's allowed actions. |
| `create_item` | Required `parent_id`, `name`; optional `id`, `content_type_id`, `values`, `keywords` | `POST /v1/resources/{parent_id}/children`, with kind fixed to item; `list.read` for parent verification and `list.write` for creation. Parent must be a readable list/library/folder. |
| `update_item` | Required `item_id`, `version`; optional `name`, `values`, `keywords`; at least one change | `PATCH /v1/resources/{id}` with quoted numeric `If-Match`; `list.read` for item verification and `list.write` for mutation. Item/folder metadata only. |
| `search` | Required nonblank `query`; optional `workspace_id`, `collection_id`, `mode`, `surface`, `limit`, `cursor` | `GET /v1/search` with `q=query`, `facets=false`, `kind=item`; `list.read`. Return selected mode, hits and backend estimate flags. |

Use `surface=auto` by default; accept `auto`, `head`, `published` and leave
authorization to the backend. `mode` is omitted by default so backend search
configuration chooses keyword/hybrid. An explicit mode is keyword/semantic/hybrid;
backend configuration/provider errors are reported rather than hidden by the
extension. Search results retain collection/workspace IDs, snippets, page,
matched-by, rank and modification metadata. Extracted file content remains
subject to the backend's `document.read` checks.

`query` uses the existing `QuerySpec` fields, including `filter`, `sort`,
`content_type_id`, parent/ancestor restrictions, search and keyword. Reuse its
documented filter tree, scalar operators and limits. Do not add a second query
compiler. Reject `group_by` in this row-returning tool; keep exact counts off.
The backend remains responsible for indexed-field eligibility and all semantic
validation. `select` limits values using the existing query projection rules.

Return compact resource DTOs: identity, kind, workspace/collection/parent IDs,
name, content type, version, timestamps, keywords and selected values. Map the
REST resource's `container_id` to the tool DTO's `collection_id` consistently.
Single-item reads and updates additionally retain backend `access`. Collection
discovery includes kind, template keys, publishing/folder settings and version;
load full schema only on demand. Do not expose persistence bookkeeping columns.

Schema discovery reads its three endpoints sequentially within the request;
use the first resource version/schema-head marker and a final resource read to
detect concurrent schema changes. If they differ, return a retryable schema
conflict instead of combining versions. Do not fetch schema history or silently
expand into the organization catalog. A published item can retain an older
schema: `get_item` returns its selected-surface values; this tool describes the
current collection schema used for new writes, not that historical schema.

The version passed to `update_item` is the resource concurrency version returned
by reads, not a content revision number. Never auto-read a newer version and
retry a failed write. Preserve the backend's patch/null behavior. Expose `id`
on create for caller-controlled retry identity using existing `CreateWithStatus`
semantics; document that repeating the same ID returns the original item and
does not apply a new payload. Never automatically retry a write after an
ambiguous network failure. A metadata-only create does not upload a file or
promise creation of file bytes.

### Pagination and results

- List/query/search return `{value, next_cursor}` with `next_cursor: null` at
  the end, retaining search/query cap and estimate flags when present. Empty
  arrays are `[]`. Return a single object for get/create/update/schema.
- Translate REST `@odata.nextLink` to its `$skiptoken`; reconstruct the next
  backend call from the original tool arguments. Never follow the returned URL
  or accept a client-supplied URL. Require its path and fixed query parameters
  to match the originating operation, ignoring the public scheme/host.
- Wrap continuation state in a size-bounded opaque encoding containing the
  tool, subject, canonical argument fingerprint and backend skiptoken. Reject
  malformed, changed-tool, changed-user or changed-query cursors. This wrapper
  is not an authorization token; the backend always rechecks current access.
- Produce identical JSON data as one text block and structured content. Keep
  custom values/filter literals as raw JSON or `json.Number` throughout;
  never round-trip them through `float64`. Apply the same rule to results.
- Expected validation, permission, missing-item and stale-version failures are
  tool results with `isError=true` and useful corrective text plus a stable
  code/request ID. Sanitize backend errors; never include tokens, SQL or paths.
  Protocol errors remain SDK JSON-RPC errors. Pre-dispatch authentication and
  admission failures remain HTTP errors.

## 5. Compiled contributions, deployment and operating limits

The contribution contract contains name, description, input/output schemas,
required scopes, read-only/destructive/idempotent/open-world annotations,
`Available(ctx, caller)` and `Call(ctx, caller, rawArguments)`. The result type
contains JSON output or a typed tool failure. The caller exposes a constrained,
request-bound backend interface; it does not expose secrets, SQL or an arbitrary
URL fetcher. Built-ins use this same contract.

Registration is explicit during executable composition; freeze the registry
before serving. Contributed names begin with the extension ID normalized by
replacing dots/hyphens with underscores, followed by `_`. Validate the complete
name and collisions at startup, including normalized-prefix collisions. All
registrations declare at least one required existing scope. A configuration
allowlist selects enabled compiled contributions; omitted contributions are
disabled. Built-ins are enabled by default. Do not add runtime installation,
tenant enablement tables or extension administration APIs. Verify availability
for both discovery and direct calls; use a test contribution to prove this
without shipping an invoice sample as a production tool.

Set read-only hints for discovery/query/get/search/schema; create/update are
non-read-only and conservatively non-idempotent, with open-world false. No delete
tool is registered. Tool annotations are advisory, never an authorization check.

Configuration is extension-owned: `PAPERGO_MCP_BACKEND_URL` (required fixed API
origin), `PAPERGO_MCP_LISTEN` (default `127.0.0.1:8081`),
`PAPERGO_MCP_PUBLIC_URL` (absolute `/v1/mcp` URL for remote ingress),
`PAPERGO_MCP_ALLOWED_ORIGINS` (additional explicit origins) and
`PAPERGO_MCP_ENABLED_EXTENSIONS` (compiled contribution IDs). Reject URL
userinfo, fragments and backend query strings. Use HTTPS except loopback or a
documented private container network. There is no backend-token setting.

Start with 32 in-flight MCP requests, at most a one-second admission wait,
a 30-second whole-request deadline, 1 MiB request bodies and an 8 MiB decoded
backend/result cap. Bound backend connections, cancel work on disconnect where
the SDK permits, and disable HTTP redirect following. Return 503/Retry-After on
admission saturation; return explicit size-limit errors rather than truncating
JSON. The module performs no file transfers requiring longer deadlines.

Emit request IDs and metadata-only logs for duration, tool, status, backend
request ID and overloads. Never log credentials, argument bodies or result data.
Send HTTP `Cache-Control: private, no-store` and use the SDK's
[`SetCacheable` hook](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/server.go)
to make caller-dependent discovery/list results private with zero TTL. This is
necessary because the SDK otherwise normalizes unset protocol cache policy.

Add unprivileged liveness/readiness endpoints; readiness checks backend health,
not a stored user credential. Graceful shutdown stops admission and drains
within the request deadline. Provide a module-owned Dockerfile and optional
Compose/reverse-proxy example. Use a private container network for proxy-to-module
traffic. A proxy connecting over loopback must rewrite Host to the loopback
upstream while the module separately validates the configured public Origin;
do not disable the SDK's localhost protection to accommodate a public Host.
Keep the current API image/startup unchanged;
include `papergo-mcp` in binary packaging and CI explicitly. Its production
dependency graph must build with `CGO_ENABLED=0` and require no PaperGo native
inference/search libraries, database file or model assets.

## 6. Implementation sequence and acceptance tests

1. **Extension foundation:** add the module contract, immutable registry, SDK
   transport, configuration, bounded caller-bound REST client and bearer
   resolver. Verify dependency isolation, request identity separation and
   private protocol caches before adding mutations.
2. **Discovery and reads:** implement workspace/collection/schema tools, query,
   get and search; add schemas, cursor translation and precise JSON handling.
   Publish complete tool descriptions and representative structured filters.
3. **Mutations:** add create/update through existing REST writes, required
   versions, safe kind checks, error translation and caller-supplied create ID.
4. **Contributions and operations:** complete enablement/availability behavior,
   limits, logs, health, shutdown, binary packaging and optional deployment
   artifacts. Add user connection documentation and link it from README.
5. **Acceptance:** exercise the module with an official SDK HTTP client against
   real PaperGo HTTP handlers, first on SQLite and then PostgreSQL. Record
   completed checks and any environment limitations before claiming completion.

Required automated scenarios:

- Discovery/initialization and tool calls through the SDK; exact schemas,
  annotations, current and earlier SDK-negotiated protocol revisions; no session
  dependence. Authenticated GET/DELETE receive the SDK's stateless behavior.
- Two users and different token scope sets concurrently: no identity/header,
  tool-list, structured-result or cache leakage. Missing/malformed credentials,
  revoked/expired tokens, role removal and inactive/deleted users fail promptly.
  Cover personal and integration tokens and the supported backend auth modes.
- Hidden tool names cannot bypass scope/availability checks. Invalid names,
  duplicate registrations and normalized prefixes fail startup. Disabling a
  test contribution removes it from listing and prevents direct execution.
- Workspace discovery, both collection kinds, folders, default/multiple content
  types, schema permissions, concurrent schema edits and schema validation
  errors. Schema discovery works for ordinary authorized users without manage.
- Structured filters/sorts/select, paging beyond one page, empty pages, malformed
  and mismatched cursors, absolute public next links and hostile next links.
  Never follow a cursor/redirect to another host or arbitrary backend path.
- Keyword search; configured semantic/hybrid modes and unavailable providers;
  snippets/pages; cap/estimate flags. ACL revocation, tombstones and unpublished
  drafts never leak through results or stale search indexes. Without
  `document.read`, file text remains inaccessible under backend rules.
- Successful create/update of generic, task and event entries and existing
  document metadata. Verify audit actor, immutable revisions, search events,
  workflow dispatch and publishing policy match an equivalent REST operation.
- Required/stale versions, field-patch omission/null semantics, protected
  integration fields, required fields, immutable schema rules and exact values
  above `2^53` plus large/precise decimal literals in input/output/filter JSON.
- Repeated caller-ID create, ambiguous write failure without automatic retry,
  forbidden parent/kind, invalid UUIDs, wrong JSON types, unknown keys and limits.
- Unavailable backend, invalid backend responses, deadlines/cancellation,
  overload, body/result limits, origin/Host protection and graceful shutdown.
  Ensure logs and user errors contain no secrets or content payloads.

Run focused module unit/race tests and a pure-Go binary build without native
setup. Put real-backend contract tests in an explicit integration test package
so their existing DMS/native fixture requirements do not infect the standalone
module build. Run those with PaperGo's normal test environment on SQLite and
`PAPERGO_TEST_POSTGRES_DSN` for PostgreSQL; preserve current DAL tests' separate
`TEST_POSTGRES_DSN` convention. Use existing fixture/schema installation helpers.
Run formatting, vet, Staticcheck and the applicable repository CI checks; no
frontend feature work is required.

Deliver `docs/mcp.md` with endpoint/deployment instructions, scope bundles,
token-revocation behavior, a header-based client configuration, the eight tools,
query examples, version conflicts, pagination and current OAuth limitations.
Document backend/proxy Authorization forwarding and write retry semantics.
Leave the REST OpenAPI contract unchanged because no REST behavior changes;
test the MCP schemas and consumed REST DTOs against that contract. Add dependency
license attribution using the repository's existing process.

Completion means all eight tools work with a real MCP client, all content
operations run under the caller's backend permissions, both database engines
pass contract coverage, the optional module deploys independently, and core/DAL
remain unchanged. OAuth sign-in, installable extensions, file transfer, extracted
text retrieval, deletion, publishing, workflow execution and new task/calendar
domain APIs remain outside this implementation.
