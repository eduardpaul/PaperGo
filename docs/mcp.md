# MCP server

`papergo-mcp` is PaperGo's optional [Model Context Protocol](https://modelcontextprotocol.io) server. It lets an MCP client, such as an AI assistant, find workspaces, lists and libraries, run their saved views, query, search, read items and the text of documents, and create and update items, with the access of the person or integration whose credential the client sends.

It is a separate process in front of the PaperGo API. It holds no data, no database connection and no credentials of its own: every tool call becomes requests to the API carrying the client's bearer credential, and the API authorizes each of them exactly as it would a direct REST call. Deployments that do not run it are unaffected. It is pure Go, needs no native libraries or models, and builds with `CGO_ENABLED=0`.

## Who can connect

- **Clients:** any MCP client that can send an `Authorization` header with a remote (Streamable HTTP) server, such as Claude Code, Cursor or VS Code. Clients that connect to remote servers only through OAuth sign-in cannot connect: the server offers no OAuth.
- **People:** in every authentication mode, proxy included, each person creates a connection under Settings → AI assistants and connects as themselves, to everything they can access or to chosen places. Administrators can also create integration tokens, limited in the same way, for a service identity with its own access.
- **Hosts:** with proxy authentication the proxy's Basic sign-in would take the `Authorization` header the assistant sends. Exempt the exact `/v1/mcp` route from it, forwarding `Authorization` unchanged to `papergo-mcp` and asserting no proxy identity there; PaperGo checks the token on every request. The [NPM sample](../samples/npm/README.md) does this, so the endpoint stays on the app's own host.

## Endpoint

The server serves the MCP [Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http) at `/v1/mcp`, statelessly: each POST is complete in itself, answers are JSON, and there are no sessions. GET and DELETE answer `405`. Protocol revisions `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26` and `2024-11-05` are negotiated.

`GET /health/live` answers `204` while the process runs. `GET /health/ready` answers `204` when the PaperGo API reports ready and `503` otherwise; it uses no credential.

## Running it

Every release archive contains `papergo-mcp` beside `papergo`. Point it at the API's origin:

```sh
PAPERGO_MCP_BACKEND_URL=http://127.0.0.1:8080 ./papergo-mcp
```

Then tell PaperGo where people's assistants reach it, so its web app offers connections: set `MCP_PUBLIC_URL` to the endpoint's public URL, such as `https://papergo.example.com/v1/mcp`, or `mcp.public_url` in `config.yaml`. It is an absolute `http` or `https` URL ending in `/v1/mcp`.

| Setting | Meaning |
| --- | --- |
| `PAPERGO_MCP_BACKEND_URL` | Required. The PaperGo API's origin, without path, query or credentials. `https`, or `http` only to a loopback or private address or a single-label host such as a Compose service name. |
| `PAPERGO_MCP_LISTEN` | Address to listen on. Default `127.0.0.1:8081`; the container image sets `0.0.0.0:8081`. |
| `PAPERGO_MCP_PUBLIC_URL` | The endpoint's public URL, ending in `/v1/mcp`, when clients reach it through a reverse proxy. `https` unless on loopback. Browser requests from its origin are accepted, and results link to the web app at its origin unless `PAPERGO_MCP_APP_URL` says otherwise. |
| `PAPERGO_MCP_APP_URL` | The web app's origin, such as `https://papergo.example.com`, that results link to. Defaults to `PAPERGO_MCP_PUBLIC_URL`'s origin; without either, results carry no links. `https`, or `http` only to a loopback, private or single-label host. |
| `PAPERGO_MCP_ALLOWED_ORIGINS` | Further browser origins allowed to call the endpoint, comma-separated, such as `https://app.example`. The web app's origin, from `PAPERGO_MCP_APP_URL` or `PAPERGO_MCP_PUBLIC_URL`, is always allowed. Clients outside a browser send no origin and need none. |
| `PAPERGO_MCP_ENABLED_EXTENSIONS` | Compiled tool contributions to serve, by ID, comma-separated. None ship today. |

There is no setting for a backend token: the server only relays its callers' credentials. Logs are JSON lines on standard output, one per request, with its request ID, status, duration, tool, failure code and the API request IDs it caused. They never contain credentials, arguments or results.

`papergo-mcp --version` prints its version. On `SIGINT` or `SIGTERM` it stops accepting connections and lets requests in flight finish, for at most 30 seconds.

### Docker and Compose

Every release publishes the server's own image for Linux amd64 and arm64, `ghcr.io/eduardpaul/papergo-mcp:vX.Y.Z` and `:latest`. It contains only the static executable; build it from the repository root with `docker build -f extensions/mcp/Dockerfile -t papergo-mcp .`.

The [Compose file](../deploy/compose.yaml) has a `papergo-mcp` service and PaperGo's `MCP_PUBLIC_URL`, both commented out. Uncommented, the server answers at `http://localhost:8090/v1/mcp`, links results to the web app at `http://localhost:8080`, and the web app offers connections to it. Behind a reverse proxy, run it on the private network without a published port and set its public URL, and PaperGo's `MCP_PUBLIC_URL`, to the proxied address:

```yaml
  papergo-mcp:
    image: ghcr.io/eduardpaul/papergo-mcp:${PAPERGO_VERSION:-latest}
    restart: unless-stopped
    environment:
      PAPERGO_MCP_BACKEND_URL: http://papergo:8080
      PAPERGO_MCP_PUBLIC_URL: https://papergo.example.com/v1/mcp
    depends_on:
      - papergo
```

### Behind a reverse proxy

Route `/v1/mcp` to `papergo-mcp` and everything else to PaperGo, on the same public host. The proxy must forward the `Authorization` header unchanged and must not cache responses. With Caddy:

```caddyfile
papergo.example.com {
	handle /v1/mcp {
		reverse_proxy papergo-mcp:8081
	}
	handle {
		reverse_proxy papergo:8080
	}
}
```

The server refuses a request that arrives over a loopback connection with a Host header that is not a loopback name, which protects a local server from DNS rebinding. A proxy that reaches it over loopback, such as one on the same machine with `PAPERGO_MCP_LISTEN=127.0.0.1:8081`, must send the upstream's own address as Host: `header_up Host {upstream_hostport}` in Caddy, `proxy_set_header Host 127.0.0.1:8081;` in nginx. Set `PAPERGO_MCP_PUBLIC_URL` so browser requests from the public origin stay accepted. Over a private container network no Host rewrite is needed.

## Authentication and scopes

Clients send `Authorization: Bearer <credential>` with every request. The server asks the API who the credential belongs to (`GET /v1/identity/me`) on every request and keeps nothing between requests. Any bearer credential the API accepts works: a [personal access token](identity.md) in a bearer-authenticated deployment, an integration token in any mode, or an OIDC access token for the API's audience. A missing or malformed credential is answered `401` with `WWW-Authenticate: Bearer realm="PaperGo"`, a refused one, such as an expired or revoked token, `401` with `error="invalid_token"` and a message to create a new token, and an inactive user `403`.

A credential's scopes decide which tools its holder sees and may call; the API's resource permissions then decide what each call may read and change, as for any REST client.

| Tool | Scopes |
| --- | --- |
| `list_workspaces` | `workspace.read` |
| `list_collections` | `workspace.read`, `list.read` |
| `get_collection_schema` | `list.read`, `contentType.read` |
| `query_items`, `get_item`, `search` | `list.read` |
| `get_item_text` | `list.read`, `document.read` |
| `list_terms` | `taxonomy.read` |
| `create_item`, `update_item` | `list.read`, `list.write` |

Two scope sets cover most assistants:

- **Read:** `workspace.read`, `list.read`, `contentType.read`, `document.read`, `taxonomy.read` and `user.read`. `taxonomy.read` and `user.read` also let results name terms and people (see [Results](#results)).
- **Read and write:** the same plus `list.write`.

Members hold all of these by default. A tool the caller lacks the scopes for is not listed, and calling it by name fails as an unknown tool. Give each assistant its own token, named after it, so revoking one leaves the others working. An assistant's changes are recorded as its token holder's, like any request with that token. Tokens expire (90 days unless set otherwise); the client then gets `401` until it is given a new one.

Revoking or expiring a token, deactivating its user or removing a role takes effect on the next request, since nothing is cached.

OAuth sign-in is not offered: the server publishes no OAuth metadata, so clients must be configured with a token. Clients that only support OAuth cannot connect.

## Connecting a client

The simplest way is **Settings → AI assistants** in the web app. It offers connections when PaperGo's `MCP_PUBLIC_URL` setting (`mcp.public_url` in `config.yaml`) names this server's public endpoint; otherwise it says that an administrator has to set one up. **Connect an assistant** asks for a name, **Read only** or **Read and write**, and whether it reaches **everything you can access** or **only selected workspaces, lists and libraries**, picked by name; **Advanced options** set when it expires. A summary shows the access, places and expiry before anything is created.

Creating the connection creates its token, shown once, and then checks the connection: the browser asks the endpoint to start a session with the new token, as an assistant would, and says whether it answered. A token that cannot reach the endpoint still exists and still works once the address is right; **Check again** repeats the check. Setup is ready to copy for Claude Code, Cursor, VS Code and other clients that send a header. **Your connections** lists the tokens that can connect an assistant with the places they reach by name, whether they have been used, when they expire or expired, and revokes them after asking.

The browser's check comes from the web app's origin. papergo-mcp accepts it from the origin of `PAPERGO_MCP_APP_URL` (or `PAPERGO_MCP_PUBLIC_URL`) and from `PAPERGO_MCP_ALLOWED_ORIGINS`, answering the browser's preflight; other origins are refused.

A connection is a personal access token, so you can also create one under Settings → Access tokens or with `POST /v1/identity/me/tokens`, with the scopes above and, to limit it, [`resources`](identity.md). The token carries the limit, so PaperGo enforces it for every request, whichever client sends it: a limited connection lists only the workspaces and collections it reaches, finds and reads nothing elsewhere, and `labels` leave out the names of linked items outside it. `GET /v1/identity/me` with the token reports its limit.

Add the server with a header. In Claude Code:

```sh
claude mcp add --transport http papergo https://papergo.example.com/v1/mcp \
  --header "Authorization: Bearer pgo_..."
```

Clients configured with JSON take the same URL and header:

```json
{
  "mcpServers": {
    "papergo": {
      "type": "http",
      "url": "https://papergo.example.com/v1/mcp",
      "headers": { "Authorization": "Bearer pgo_..." }
    }
  }
}
```

Treat the token as a password: the client stores it, and it acts as you.

## Tools

| Tool | What it does |
| --- | --- |
| `list_workspaces` | The workspaces the caller can read. |
| `list_collections` | A workspace's lists and libraries, with their template keys, publishing and folder settings and version. |
| `get_collection_schema` | A list's or library's settings, fields (key, label, type, required, choices, decimal scale, whether indexed), attached content types with their rules and which is the default, and its saved views. |
| `query_items` | Items and folders of a collection, filtered, sorted and selected with PaperGo's [query language](application-foundation.md#typed-queries-and-saved-views), or as one of its saved views shows them. |
| `get_item` | One item or folder with all its values and the caller's `access`. |
| `get_item_text` | The text PaperGo read from a document's file, page by page. |
| `list_terms` | The terms a term or keywords field can hold, or the organization's keywords. |
| `create_item` | Creates an item in a list, library or folder. |
| `update_item` | Changes an item's or folder's name, values or keywords. |
| `search` | Searches items across workspaces by relevance. Listed only while the deployment runs [search](search.md). |

Every tool declares its input and output JSON Schemas. Arguments are checked against them before anything runs: unknown properties, wrong types, malformed IDs and out-of-range limits fail as `invalid_arguments`. Read tools are marked read-only; `create_item` and `update_item` are marked neither read-only nor idempotent. These hints are advisory; the scopes and the API decide. There is no tool to delete, move, archive or publish.

### Results

Items are returned with `id`, `kind`, `workspace_id`, `collection_id`, `parent_id`, `name`, `content_type_id`, `version`, creation and update times and actors, `keywords` (term IDs) and `values` by field key. `surface` chooses the revision read: `head` for the latest draft, `published`, or `auto` (the default), which is the head where the caller may read drafts. Numbers keep every digit: integers above 2^53 and decimals pass through as written.

Term, keywords, person and lookup fields hold IDs, and so do `keywords`, `created_by` and `updated_by`. `labels` names them by ID, so an assistant can say "Acme" rather than an ID: term and keyword names with `taxonomy.read`, people's display names with `user.read`, and the names of linked items the caller can read. Names the caller may not read are left out; they never fail the call. A page resolves them in a few batched API requests.

`draft` is true while an item has changes that are not published: people who read only published content do not see them. `archived` is true for archived entries. Workspaces, collections, items and search hits carry a `url` that opens them in the web app, when the server knows its address (`PAPERGO_MCP_APP_URL`).

### Querying

`query_items` takes the collection's `collection_id` and an optional `query`:

```json
{
  "collection_id": "…",
  "query": {
    "filter": { "and": [
      { "field": "status", "op": "eq", "value": "Open" },
      { "field": "amount", "op": "gte", "value": 1000.50 }
    ] },
    "sort": [{ "field": "$modified_at", "direction": "desc" }]
  },
  "select": ["status", "amount"],
  "limit": 20
}
```

Filters and sorts on custom fields need the field indexed; a query that names another field fails with the list of fields it can use. `value_ref` replaces `value` with `me` or a relative date such as `today` or `last7Days`. `under` matches a term and the terms below it. `parent_id` keeps entries directly in a folder, `ancestor_id` entries anywhere below it, `search` matches words, `keyword` a keyword, and `archive` chooses `active` entries (the default), `archived` ones or `all`. `select: []` returns no custom values. Grouping and totals are not offered.

`view_id` runs one of the collection's saved views instead of `query`, with its filter, sort and columns, as people see it in the web app; `get_collection_schema` lists the views. Navigation views browse terms and cannot run this way; filter their fields with `under` instead.

### Documents and terms

`get_item_text` returns the text PaperGo read from a document's current file: pages from `page` (default 1) up to `max_chars` characters (default 20,000, at most 100,000), and `next_page` to continue from. A page longer than `max_chars` is cut and marked `partial`. A file whose text has not been read yet, because no [workflow](workflows.md) with `document.read_text` ran for it, fails as `not_found`.

`list_terms` lists terms so assistants use existing ones instead of guessing labels: for a term or keywords field (`collection_id` and `field_key`, `$keywords` for the keywords every entry has) or a `term_set_id`. Without `search` or `parent_id` it lists top-level terms; `parent_id` lists a term's children and `search` matches names, labels and synonyms.

### Pages

Listing tools return `{"value": [...], "next_cursor": ...}`. Pass `next_cursor` back as `cursor`, with every other argument unchanged, for the next page; it is `null` on the last page. A cursor belongs to the tool, caller and arguments that produced it: using it with anything else fails as `invalid_arguments`. It grants nothing; the API checks every page. `limit` is 1 to 200, default 50; for `search`, 1 to 50, default 10.

### Writing

Term and keywords values, and `keywords`, take a term's ID or label. A keyword label PaperGo does not know adds a keyword for the whole organization and needs `taxonomy.read`; the tool descriptions ask assistants to prefer existing keywords from `list_terms`.

`update_item` requires the item's `version`, from `get_item`, `query_items` or a previous write. When the item changed since, the update fails with `version_conflict` and changes nothing; read it again and decide whether the change still applies. `values` changes only the fields it names, and `null` clears one. `keywords` replaces the whole set.

`create_item` creates in the collection's default content type unless `content_type_id` names another attached type. Give it an `id` of your own (a UUID) to make it safe to repeat: when an item with that ID already exists there, it is returned unchanged with `created: false` and the new arguments are ignored. Without an `id`, repeating a create makes a second item.

The server never repeats a write. When the API's answer is lost, the call fails as `unavailable` and the write may or may not have happened: repeat a create with the same `id`, and re-read before repeating an update.

Writes are the caller's, with the same audit actor, revisions, publishing policy, workflows and search updates as the equivalent REST request. In a collection with publishing, creates and updates are drafts until someone publishes them in the web app: their results have `draft: true`, and the tool descriptions ask assistants to say so.

### Search

`search` hits carry their workspace and collection with their names, a snippet around the first match with the matches marked «like this», the file page the snippet is on, how the hit matched, its rank and its `url`.

### Failures

A tool that cannot do what was asked returns an error result whose text is JSON:

```json
{"code": "version_conflict", "message": "…", "request_id": "…"}
```

| Code | Meaning |
| --- | --- |
| `invalid_arguments` | The arguments or the cursor are wrong, the ID names another kind of resource, a query names a field that is not indexed, or the API rejected the values. |
| `forbidden`, `not_found` | The caller may not do this, or the resource does not exist for them. |
| `version_conflict` | The item changed since the given version. |
| `conflict` | The write conflicts with existing content, such as a create `id` used for another item. |
| `schema_changed` | The collection's schema changed while `get_collection_schema` read it; call again. |
| `too_large` | The answer is larger than 8 MiB; ask for fewer entries or values. |
| `unavailable` | PaperGo did not answer in time or at all; `retryable` is set. |
| `unauthorized`, `internal_error` | The credential stopped working mid-request, or an unexpected failure. |

`request_id` names the PaperGo API request that failed, or the MCP request when no API request did, for matching with logs.

## Limits

The server handles 32 requests at a time; a request that waits more than one second for a turn is answered `503` with `Retry-After`. Each request may take 30 seconds. Request bodies are limited to 1 MiB and each API answer and tool result to 8 MiB. It keeps at most 64 connections to the API. Every response is `Cache-Control: private, no-store`, and tool listings are marked private with no cache lifetime, since they depend on the caller.

## Terms

The tools use PaperGo's API names, which differ from some web app labels:

| Tool term | In the web app |
| --- | --- |
| collection | a list or a library |
| item, entry | an item, or a document in a library; entries are items and folders |
| surface `head` / `published` | the latest draft / the published version |
| term | a term of a taxonomy (term set) |

## Contributions

Further tools are compiled into `papergo-mcp` as contributions of the `extensions/mcp` package, each under an ID whose tools are named with that ID, dots and hyphens replaced with `_`, followed by `_` (contribution `acme.crm` names tools `acme_crm_…`). A deployment serves a contribution only when `PAPERGO_MCP_ENABLED_EXTENSIONS` names it. Contributions use the same contract as the built-in tools: they declare their schemas and scopes, may report themselves unavailable to a caller, and reach PaperGo only through the API as that caller. The server refuses to start with an invalid or colliding name, a tool without scopes or schemas, or an enabled ID that is not compiled in.
