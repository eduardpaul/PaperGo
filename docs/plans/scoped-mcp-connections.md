# Scoped AI assistant connections

Status: approved 2026-10-23; in progress on `claude/mcp-server`.

## Story

> As a PaperGo user, I want to connect an AI assistant to everything I can
> access, or only to the workspaces, lists and libraries I choose, with
> read-only or read-and-write access, so that I can use an assistant on my
> work without exposing more than it needs.

A connection is a personal access token plus the MCP endpoint, created in one
simple step. People do not start servers: the deployment runs one MCP
endpoint, and every connection's limits travel with its token.

Decisions:

- **Global connections are available by default.** "Everything I can access"
  is the simple path; limiting to places is an advanced option.
- **Places are workspaces, lists and libraries.** A workspace includes the
  lists and libraries created in it later. Folders are not a unit.
- **One access level per connection:** read only, or read and write.
- **The endpoint is the app's own host:** `https://<app host>/v1/mcp`, the
  documented layout. There is no setting for another address.
- **The API enforces the limit, not papergo-mcp.** A limited token is
  limited everywhere: REST, WebDAV, queued work and MCP alike.

## Core change: resource limits on access tokens

Approved under the Extension-First protocol. It is generic: the same limit
serves MCP connections, single-list integration tokens and WebDAV tokens.

- **Schema.** `access_tokens.resources`: a JSON array of workspace, list and
  library IDs, empty for an unlimited token. Both schema masters; Bob models
  regenerated.
- **Identity.** `AccessTokenInput`, `AccessToken` and the authenticated
  `AccessTokenIdentity` gain `resources`. Creation checks at most 50
  distinct IDs, each a live workspace, list or library the caller can read.
  A limited token cannot hold organization-wide scopes (`workspace.create`,
  `workspace.manage` and every `*.manage`, `role.*`, `audit.read`); a
  limited token cannot create tokens. `GET /v1/identity/me` reports the
  request's limit.
- **Authorization.** `access.Credential` carries the limit. `Resource` and
  `Rows` add one predicate: a row is in the limit when its workspace, its
  collection or itself is listed. A list's or library's workspace is
  readable, so clients can navigate to it, but not writable or manageable.
  Organization scopes are unaffected; content is.
- **Queued work.** Operations and integrations record the credential ID and
  scopes today; when they run, the live token's limit is loaded with them,
  so queued work stays inside the limit.
- **Search.** Its permission prefilter samples one item per permission scope,
  which can span lists, so a limited search computes scopes without the
  limit and rechecks every hit with it. When the limit is a single
  workspace or collection, that place is also passed to the index as a
  filter. With several places, pages can come back short and totals count
  matches outside the limit; a store-level set filter is a follow-up.

## MCP

papergo-mcp needs no new rules: every API call it makes is limited. Tools
already list only what the API returns, labels leave out names the caller
cannot read, and write tools are hidden without `list.write`. The docs
explain connections and limits.

## Web app: Settings → AI assistants

Simple first:

1. **Connect an assistant.** Name (defaults to "AI assistant"), access
   **Read only** or **Read and write** (read only preselected), and
   **Everything I can access** preselected.
2. **Advanced options**, collapsed: **Only selected places** with a
   searchable picker of workspaces, lists and libraries grouped by
   workspace; expiry in days (default 90).
3. **Created.** The endpoint, the token shown once, and copy-ready setup for
   Claude Code and for JSON-configured clients.
4. **Your connections.** Name, access ("Read only · 2 places" or
   "Read and write · Everything"), created, last used, expiry, Revoke.

Read only maps to `workspace.read`, `list.read`, `contentType.read`,
`document.read`, `taxonomy.read` and `user.read`; read and write adds
`list.write`. Scopes the person does not hold are left out. Tokens have no
kind, so **Your connections** lists every personal token that reads lists:
any of them can connect an assistant.

## Phases

1. Core: schema, models, identity, authorization, queued work, search,
   `/v1/identity/me`; DAL, identity and HTTP tests on both engines.
2. Contract: OpenAPI, regenerated SDK, `docs/identity.md`.
3. MCP: an integration test of a limited connection; `docs/mcp.md`.
4. Web: the AI assistants page and its tests.

## Acceptance

- A token limited to Invoices lists only Invoices' workspace and Invoices
  through REST and MCP; queries, reads, search and writes elsewhere fail as
  not found or forbidden; its labels leave out names from other lists.
- A token limited to a workspace sees lists created there later.
- A read-only connection offers no write tools, and REST writes fail.
- A limited token cannot create workspaces, tokens, or manage anything
  organization-wide, and cannot be created with such scopes.
- Queued work started with a limited token stays inside its limit.
- Losing access to a place removes it from the connection immediately.

## Later

Changing a connection's places without a new token; admin policy to turn
off global connections or cap expiry; recording the credential on audit
events; a store-level search filter for several places.
