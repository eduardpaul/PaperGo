# Reverse proxies and identity providers

PaperGo runs at the root of one public host behind a TLS-terminating reverse proxy. Choose OIDC bearer authentication or trusted proxy authentication. Both modes enroll identities in the same persisted organization directory and apply the same roles and resource ACLs. The [NPM Compose sample](../samples/npm/README.md) runs with NPM Access List authentication and no separate identity provider.

## Authentication and directory

| Area | Current behavior |
| --- | --- |
| Provider bearer tokens | OIDC verifies signature, issuer, expiry and audience. `OIDC_TOKEN_TYPE` optionally checks the JWT header type. |
| Personal REST tokens | Bearer-authenticated deployments accept scoped personal credentials. These requests preserve the stored profile and group memberships. |
| Users | Verified subjects enroll with the Member role on first use. OIDC profile claims supply a display name and email; proxy profiles are locally managed. |
| Groups | Local groups are administered in PaperGo. OIDC groups synchronize from provider claims; their direct memberships are managed by the provider. |
| Roles and ACLs | Both group sources use the same persisted role assignments, nested-group expansion and content grants. |
| First administrator | Exactly one of `ADMIN_SUBJECT` and `ADMIN_GROUP` is required in OIDC mode and applied once at initialization. |
| WebDAV | OIDC deployments use provider bearer tokens or Basic app passwords. Proxy deployments use proxy-verified usernames. App-password requests preserve stored memberships. |
| Public links | `PUBLIC_URL` supplies the public scheme and host for absolute continuation links. Without it, links use the request origin. |
| WebDAV destinations | COPY/MOVE accept the request host or the configured public host, within the same library. |
| Trusted proxy identities | `AUTH_MODE=proxy` accepts a verified username only from configured peers carrying the shared proxy secret. Generic `Remote-User`, `Remote-Groups` and forwarded client-address headers are ignored. |

HTTP adapters verify credentials and pass the verified identity to `internal/identity`. Profile and membership changes, their audit records and the sync hash commit in one directory transaction. Identity membership audits also feed the existing live-access change notifications. Content operations continue checking current persisted scopes and ACLs in their own transactions.

## NPM-only authentication

Set `AUTH_MODE=proxy`, `ADMIN_SUBJECT` to an NPM Access List username, `PUBLIC_URL` to the HTTPS origin, `PROXY_SECRET_FILE` to a private secret file, and `PROXY_TRUSTED_CIDRS` to the NPM backend address with a /32 (or an explicit bounded proxy network). Use a random secret of at least 32 bytes and keep PaperGo's backend port private.

NPM validates Basic credentials and overwrites `X-Papergo-Subject` with `$remote_user` and `X-Papergo-Proxy-Secret` with the configured secret in every forwarded location. Use `satisfy all` and disable authorization passthrough. PaperGo requires both the actual network peer and the secret, rejects duplicate or empty identity headers, and rejects forwarded client `Authorization`. It never derives trust from `X-Forwarded-For`. Batch requests cannot override the identity assertion. Browser mutations must match the configured public origin through Origin/Referer validation and cannot carry cross-site Fetch Metadata.

NPM Access List usernames are the stable PaperGo subjects. NPM management accounts administer the proxy; they do not become application users. Groups, profiles and roles are managed in PaperGo. Removing access-list credentials blocks subsequent requests; disabling the PaperGo account also revokes active application access. Rename usernames by explicitly creating a different identity rather than transferring another user's permissions.

In this mode REST and WebDAV clients use their NPM Basic credentials over HTTPS. PaperGo personal REST-token and WebDAV app-password endpoints are unavailable (404), and OIDC settings and `ADMIN_GROUP` are rejected. The sample provisions a dedicated backend network, static trusted proxy address, shared secret, TLS, persistent volumes and health checks.

## Public origin

Set `PUBLIC_URL` to the origin clients use, for example `https://docs.example.com`. Production requires HTTPS. A path component is rejected; serve PaperGo at the root of its own host.

When a proxy forwards an HTTP request after terminating TLS, the configured origin keeps absolute `@odata.nextLink` URLs on HTTPS. WebDAV accepts this host in `Destination` even when the proxy rewrites the upstream `Host` header. PaperGo ignores `X-Forwarded-Proto` and `X-Forwarded-Host` for this purpose.

## Group synchronization

| Setting | Default | Meaning |
| --- | --- | --- |
| `OIDC_GROUPS_CLAIM` | empty (off) | Claim holding group names as a string array or comma-separated string. Dotted paths reach nested claims such as `realm_access.roles`. |
| `OIDC_GROUPS_PREFIX` | empty | Only names with this prefix are synchronized, with the prefix removed (`papergo-finance` becomes `finance`). |
| `OIDC_GROUPS_FROM_USERINFO` | `false` | Fetch UserInfo when the groups claim is absent from the access token. |

On a request containing the configured groups claim, PaperGo creates missing groups with `source='oidc'` and synchronizes the user's direct OIDC memberships with that claim. An empty claim removes those memberships, subject to last-administrator protection. An absent claim preserves memberships unless UserInfo supplies it. Local groups and their memberships are untouched. A claim matching a local group is skipped and logged; an administrator can rename the local group to resolve the collision.

Group names are limited to 200 UTF-8 bytes without surrounding whitespace. At most 100 valid distinct groups are synchronized per token. Direct membership writes on OIDC groups return 409 `group_managed_by_identity_provider`. Administrators can edit their names and descriptions, delete them, assign roles and ACL grants, or nest them inside local groups.

Membership removals that would leave no active administrator are deferred and logged. Subsequent provider-token requests retry them even when claims are unchanged, so removal takes effect after another active administrator exists. Deferred removals do not produce an audit event for a membership that remains present.

A hash of normalized profile and group claims avoids writes for an unchanged, completely applied identity. Effective profile changes, group creation and membership changes produce identity audit records in the same transaction; hash-only updates do not. An audit failure rolls back the whole synchronization.

## Profiles and administrator bootstrap

The display name uses the token's `name`, then `preferred_username`, then its verified subject. Email comes from the token's `email` claim. Profile reads at `/v1/identity/users` and `/v1/identity/me` expose the stored name and email. `/v1/people` returns active subject IDs. Names and emails are display data; authorization uses the stable subject.

`ADMIN_SUBJECT` names the verified subject of the first administrator. Alternatively, `ADMIN_GROUP` requires `OIDC_GROUPS_CLAIM` and creates an OIDC group assigned the Administrator role. With `OIDC_GROUPS_PREFIX`, use the group name after prefix removal: for IdP group `papergo-admins` and prefix `papergo-`, set `ADMIN_GROUP=admins`.

Bootstrap runs once. Until the first member of `ADMIN_GROUP` signs in, that configuration has no effective administrator. Changing bootstrap settings after initialization does not grant new administrator assignments. Development authentication defaults `ADMIN_SUBJECT` to `DEV_SUBJECT`.

`GET /v1/identity/me` returns the subject, stored profile, direct group names and effective scopes so clients can inspect their identity.

## Token type and UserInfo

Set `OIDC_AUDIENCE` to the intended API audience. Where supported, a resource identifier such as `https://docs.example.com` separates API access tokens from a client's ID tokens. `OIDC_TOKEN_TYPE=at+jwt` additionally requires a matching JWT header type; configure it only for an issuer that supplies that type.

With `OIDC_GROUPS_FROM_USERINFO=true`, a token lacking the groups claim queries the issuer's UserInfo endpoint using the same bearer token. UserInfo can also supply missing profile claims. Results are cached per token until expiry. The cache holds at most 1,024 tokens, prunes expired entries when storing a result, and evicts the earliest-expiring entry when full. An evicted token fetches UserInfo again on its next request. JWT access tokens are required; opaque-token introspection is unsupported.

## Deployment limits

PaperGo uses one organization per database. It does not provide SCIM provisioning or client-IP authorization. Trusted proxy authentication uses explicitly authenticated proxy assertions; it does not consume arbitrary remote-user headers. Removing a user at the provider stops new tokens; already-issued JWTs remain valid until expiry. Administrators can deactivate a user in PaperGo to end access immediately. Provider removal does not automatically delete or deactivate the stored directory row.

## Proxy configuration

### Rules for every proxy

1. **Use the authentication-mode contract.** In OIDC mode, pass `Authorization` unchanged and leave access lists disabled. In proxy mode, require NPM Basic authentication, strip `Authorization` upstream, and install the trusted assertion headers in every location.
2. **Keep the `Host` header**, or set `PUBLIC_URL`. NPM (`proxy_set_header Host $host`), Caddy and Traefik keep it by default.
3. **Pass the URI as received.** WebDAV names carry percent-encoding. NPM proxies `$request_uri`, which is correct. Do not normalize or rewrite paths.
4. **Allow WebDAV methods**: `OPTIONS`, `PROPFIND`, `PROPPATCH`, `MKCOL`, `COPY`, `MOVE`, `LOCK`, `UNLOCK`, `PUT`, `DELETE`. nginx, Caddy and Traefik pass them by default; WAF rules and Cloudflare page rules may not.
5. **Body size** at least `MAX_UPLOAD_BYTES` plus multipart overhead: 80 MB for the 64 MiB default.
6. **Stream transfers.** Turn off request and response buffering for this host. PaperGo already exempts transfers from its in-flight limit and extends their deadlines. Buffering at the proxy only adds a disk copy of every upload and delays downloads.
7. **Timeouts** of at least 300 s. Reads stop at 30 s inside PaperGo, but large mutations (recursive delete, bulk publish, template adoption) run longer. A proxy 504 while PaperGo commits leaves the client unsure. Retries are safe where PaperGo uses `If-Match` or caller IDs.
8. **One host per PaperGo** (see below).
9. **Do not publish PaperGo's port.** Only the proxy reaches it.

### One host per PaperGo

Serve PaperGo at the root of its own host name (`docs.example.com`), not under a path (`example.com/papergo/`):

- WebDAV `href`s and the `/webdav/` prefix are fixed.
- Windows and Office discover WebDAV with `OPTIONS /` at the server root.
- `@odata.nextLink` and `Destination` are built from the request path.

### Nginx Proxy Manager

**Proxy host:**

| Tab | Setting |
| --- | --- |
| Details | Domain: `docs.example.com`. Scheme `http`, forward host `papergo`, port `8080`. **Cache Assets: off.** **Block Common Exploits: off**: it answers 403 to query strings with `concat(`, `union … select (` and similar, which search terms and filter values can contain. **Websockets Support: on**: no websockets are used, but it switches the upstream to HTTP/1.1, which unbuffered chunked uploads need (macOS Finder uploads with chunked encoding). Access List: **Publicly Accessible** for OIDC mode; use the sample’s protected Access List for proxy mode. |
| SSL | Let's Encrypt certificate, **Force SSL on**, HTTP/2 on, HSTS on. |
| Advanced | The block below. |

```nginx
# PaperGo: uploads up to MAX_UPLOAD_BYTES (64 MiB) plus multipart overhead.
client_max_body_size 80m;
# Stream uploads and downloads instead of copying them to NPM's disk first.
proxy_request_buffering off;
proxy_buffering off;
proxy_http_version 1.1;
# Long mutations (recursive delete, bulk publish) can take longer than NPM's 90 s.
proxy_read_timeout 300s;
proxy_send_timeout 300s;
```

NPM's defaults, for reference: `client_max_body_size 2000m`; connect, send and read timeouts of 90 s; `proxy.conf` sets `Host`, `X-Forwarded-Proto`, `X-Forwarded-For` and `X-Real-IP`. PaperGo ignores the forwarded headers. With `PUBLIC_URL` set it does not need them.

**The identity provider on the same NPM.** PaperGo fetches OIDC discovery and keys from `OIDC_ISSUER` at startup and must use the **public** URL, because the issuer in the tokens is the public one and production requires `https://`. When PaperGo and NPM share a Docker network, give NPM a network alias with the IdP's host name, so PaperGo reaches it without hairpin NAT and with a valid certificate:

```yaml
services:
  npm:
    networks:
      papergo:
        aliases: [auth.example.com]
  papergo:
    image: papergo
    restart: unless-stopped       # retries until the IdP answers discovery
    environment:
      APP_ENV: production
      AUTH_MODE: oidc
      OIDC_ISSUER: https://auth.example.com
      OIDC_AUDIENCE: https://docs.example.com
      PUBLIC_URL: https://docs.example.com
      OIDC_GROUPS_CLAIM: groups
      ADMIN_GROUP: papergo-admins
    volumes: [papergo-data:/data]
    networks: [papergo]           # no ports: only NPM reaches it
```

The IdP is its own NPM proxy host (`auth.example.com`) with no special settings.

### Caddy

```caddy
docs.example.com {
	request_body {
		max_size 80MB
	}
	reverse_proxy papergo:8080 {
		flush_interval -1
		transport http {
			read_timeout 300s
			write_timeout 300s
		}
	}
}
```

Caddy streams request bodies and keeps `Host` by default.

### Traefik

Use a router on `Host(\`docs.example.com\`)` with TLS and the service `http://papergo:8080`. Keep `passHostHeader` at its default (`true`). Raise `respondingTimeouts` on the entry point (Traefik v3 defaults `readTimeout` to 60 s, which cuts long uploads) and do not attach `basicAuth` or `forwardAuth` middlewares to this router.

### Cloudflare in front

The free plan limits request bodies to 100 MB and cuts responses after 100 s without data. Uploads at PaperGo's default limit fit. Long mutations may be cut; keep them below 100 s or bypass Cloudflare for the host. WebDAV methods pass through Cloudflare's proxy.

## Identity providers

What each IdP needs so that PaperGo receives a **JWT access token** with the right `aud`, a stable `sub` and a groups claim. Clients get tokens from the IdP: authorization code + PKCE for apps and a future web UI, the device flow for CLIs, client credentials for services (their `sub` is the client and is enrolled like a user).

| IdP | Access token | `OIDC_AUDIENCE` | Groups | Notes |
| --- | --- | --- | --- | --- |
| **Authelia** | Opaque by default. Set the client's `access_token_signed_response_alg` (for example `RS256`) for RFC 9068 JWTs. | An entry from the client's `audience` list, requested with the `audience` parameter (`requested_audience_mode: implicit` grants it without asking). | `groups` scope, copied into the access token by a `claims_policies.<name>.access_token: [groups]` policy on the client. *Not verified on a running instance.* | Supports `OIDC_TOKEN_TYPE=at+jwt`. File or LDAP users; two-factor and passkeys. |
| **Authentik** | JWT. | The provider's client ID (no separate resource audience). | `groups` from the default `profile` scope mapping. *Not verified on a running instance.* | No `at+jwt` type. Give PaperGo its own provider, so no other client's ID tokens carry its audience. |
| **Keycloak** | JWT. | Add an *Audience* mapper to the client scope (`aud = docs.example.com` or a resource client ID); the default `aud` is `account`. | *Group Membership* mapper → claim `groups`, "Full group path" off. Realm roles: `OIDC_GROUPS_CLAIM=realm_access.roles`. | `sub` is a UUID: use `ADMIN_GROUP`. |
| **Pocket ID** | JWT. | Client ID. *Not verified.* | `groups` scope. *Not verified.* | Passkey-only; the smallest option for people who only want "NPM plus users". |
| **Microsoft Entra ID** | JWT for an exposed API (`api://…`). | The API's application ID URI. | `groups` claim with group **object IDs**, not names; more than 200 groups produce an overage claim. | Prefer app roles: `OIDC_GROUPS_CLAIM=roles` carries role names. |

For any IdP: decode a real access token (`jwt.io`-style, locally) and check `iss`, `aud`, `sub` and the groups claim before configuring PaperGo.

## WebDAV through a proxy

- Clients use **app passwords** (`POST /v1/webdav-credentials`) with Basic auth. The proxy must pass `Authorization` (rule 1). In OIDC mode, people create them with their bearer token. In proxy mode, clients use NPM Access List credentials instead.
- Windows' WebClient sends Basic credentials only over HTTPS (default `BasicAuthLevel=1`), which the proxy provides, and limits downloads to 50 MB by default (`FileSizeLimitInBytes`).
- Group sync does not run on app-password requests: they carry no token. Their memberships are whatever the user's last token set.

## Verification

- `PUBLIC_URL`: next links use it; POST continuation through nginx with Force SSL; `Destination` with the public host and a rewritten `Host`.
- Group sync: creation, add and remove; local groups untouched; read-only membership API for oidc groups; name collision with a local group; nesting oidc in local groups; ACL and role effects on the next request; last administrator kept; hash short-circuit (no write when unchanged); 100-group limit; prefix filter; dotted claim paths.
- `ADMIN_GROUP`: initialization, first member becomes administrator, exactly-one-of validation.
- Token type check: an ID token is rejected with `OIDC_TOKEN_TYPE=at+jwt`.
- Deployment validation through a configured proxy: API through the proxy, a 64 MiB upload and download with buffering off, a chunked WebDAV `PUT`, `COPY`/`MOVE`, a request longer than 90 s.
