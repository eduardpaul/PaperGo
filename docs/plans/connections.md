# Connections (plan)

Status: phases 1 to 3 are built on `feature/connections` as this plan describes: MQTT and HTTP connections, live subscriptions, workflow operations, `connection.message` triggers (S3), package and solution parts (S5), apps, settings, docs ([connections](../connections.md)) and the [Home sample](../../samples/home/README.md).

## Why

Apps and workflows reach only the PaperGo API:
- An app's frame has no network (`connect-src 'none'`).
- A workflow script has no network.

To read sensors, drive devices or call a Node-RED flow, PaperGo itself holds the connection, and apps and workflows reach it through the API. That way the API keeps the credentials, applies the permission checks and records the audit.

The app guarantee changes from "no network" to: **an app reaches outside PaperGo only through channels a workspace manager opened, as people who may use them, and never sees an address or a credential.** Publishing and calling send data outside, so channels are what managers open deliberately. What an app sends out is capped (16 KiB a message or request).

Rejected:
- **Opening the frame's CSP** to the broker or Node-RED. It puts credentials in the browser, allows any destination, and runs into CORS and mixed-content problems.
- **A generic proxy route** (`/v1/proxy?url=`). It lets an app author reach any address on the server's network.
- **Putting messages on the durable live journal** (`internal/dal/live`). It is rows polled every second, and telemetry would flood it.

## What a connection is

A **connection** is an outside endpoint that belongs to one workspace, as an app or a workflow does. Its `kind` is `mqtt` or `http`.
- People who manage the workspace and hold `connection.manage` set it up.
- Apps and workflows use it by **key**; they never see its target or credentials.
- One connection serves one workspace. Separate credentials per workspace keep the endpoint's own ACLs tight, and borrowed access (below) stays inside the workspace.

Every kind has the same frame:

| Field | Meaning |
| --- | --- |
| `key` | from the name unless given; unique in the workspace; never changes |
| `name`, `kind`, `enabled` | |
| `target` | where it goes; shown to managers only |
| `tls.ca` | optional PEM, for self-signed certificates on the local network |
| `credentials` | write-only; kept encrypted; shape depends on the kind |
| `channels` | what callers may reach, and who may (up to 50) |

### Channels

A **channel** is a named, bounded part of what the connection reaches. Its shape depends on the kind (MQTT: a topic filter; HTTP: methods and a path pattern), but every channel works the same way:
- **Callers name channels.** Apps and workflows say `home` and `heating`, never a raw address.
- **Channels bound the credential.** Whatever is outside every channel is unreachable through PaperGo, whatever the endpoint itself allows.
- **Every use is either *see* or *control*:**
  - MQTT subscribing, and HTTP `GET` and `HEAD`, are *see*.
  - MQTT publishing, and HTTP `POST`, `PUT`, `PATCH` and `DELETE`, are *control*.
- **No overlaps.** Two channels may not both cover the same thing for the same use, the same topic or the same method and path. Saving refuses this and names both channels, because otherwise a wide channel would open a narrow, restricted one.
- **Each channel carries its own permissions** (see below).

## Permissions

Requirements: easy for a family or a small office ("members see the sensors and switch the lights"), and flexible for organisations ("only Facilities drives the boiler"). It must do this without a second permission system.

The rule: **a connection borrows its permissions from PaperGo resources.** It keeps no ACL of its own, and the rules are the same for every kind.

### Three layers

1. **Managing.** Creating, changing and deleting a connection, and seeing its target, status and monitor, need:
   - workspace manage access;
   - `connection.manage`.

   The Member role does not grant `connection.manage`; administrators have it, as with `app.manage`, since a connection can reach the server's local network. Limited tokens cannot hold it.
2. **Module scope.** Control also needs `connection.control`. The Member role grants it and limited tokens may hold it (it is added to `LimitableScopes`).
   - A read-only token never drives a device or calls a flow that changes something.
   - A token that only edits content (`item.write` without `connection.control`) does not either.

   *See* needs no scope of its own: the borrowed read access already needs `workspace.read` or `item.read`.
3. **Channel access.** A channel has an `access` for each use, `see` and `control`, each `{ level, on }`:
   - `level` is `read`, `write` or `manage`, as in a workflow manual trigger's `access`.
   - `on` is `workspace` (the default), or a **list or library** of the same workspace.
   - The check is `dms.Service.Authorize(subject, on, level)`. It is the transaction-time ACL check items use, and it brings that check's module scope: `write` also needs `item.write`, and `manage` needs `list.manage`.

| Default | Who |
| --- | --- |
| see: `{ level: "write", on: "workspace" }` | members and owners |
| control: `{ level: "write", on: "workspace" }` | members and owners |

*See* defaults to members, not visitors. Presence, motion and alarm topics, and flows that report the house, tell when nobody is home, and visitors are often outsiders. Widening a channel to visitors is one choice; narrowing after a leak is too late.

**Easy:** a new connection works for the workspace's members with nothing to set.

**Flexible:** point a use at a list.
1. Make a "Facilities" list.
2. Break its inheritance and grant the Facilities group `write`.
3. Choose it for the heating channel's control access.

Groups, nested and OIDC groups, workspace-role principals and broken inheritance then all apply. They are managed in the existing Permissions dialog and evaluated live.

**What the settings say**, in plain words:
- "Who can see: members of the workspace / people who can see *list* / …".
- "Who can control: members / people who can edit *list* / …".
- **Workspace owners can always see and control.** Owners keep manage access to everything in their workspace, and so does `workspace.manage`.
- **Workflows act as their author.** A workflow whose manual trigger lets members start it can drive a channel for them.
- If the list `on` names is deleted, that use refuses (`403 connection_access_missing`) and the settings name the problem. Restoring the list restores access.
- Saving checks that the manager can read the list chosen.

**Who is checked.** An app is checked as its viewer, a workflow as its version's author and a token as its holder, intersected with the token's scopes. A limited token's place limits apply to the resource `on` names: a token limited to one list may control a channel borrowed from that list, and not one borrowed from the workspace.

**The endpoint does not know the person.** The broker and Node-RED see PaperGo's credential, not who asked. PaperGo has decided access before the request leaves. An HTTP connection can pass the caller on in signed headers (see [HTTP](#http)) for the endpoint's logs or its own rules.

### Deferred

- **Folders and items as `on`.** An inheriting folder or item changes its effective permissions silently when it moves inside its list, so it is too fragile for control. This waits until there is a reason.
- **Item-bound channels.** A topic or path template over an item's field (`home/{field:device_id}/set`, `/devices/{field:device_id}/set`) with `on` a list: using an item's topic or path needs access to that item, and a "Devices" list becomes the device registry. It builds on layer 3 without changing it.

## Network

Connections reach what the operator allows. Every connection, reconnection and HTTP request is checked on the resolved address through the guarded dialer:
- **Public addresses:** must be on the [trusted hosts](../trusted-hosts.md) list and use TLS (`mqtts`, `wss`, `https`).
- **Every non-public address**, which means RFC 1918, ULA, loopback, CGNAT/Tailscale `100.64.0.0/10`, Docker's `172.16.0.0/12` and any other range the calendar's public-address check refuses, is refused unless the operator lists it:
  - `connections.networks` in `config.yaml`, or `CONNECTION_NETWORKS`, such as `192.168.1.0/24,100.64.0.0/10`;
  - inside those networks, plain `mqtt`, `ws` and `http` are allowed.

  Trusted hosts never open private networks. This operator setting does, as the operator already decides AI endpoints.
- **Always refused:** link-local, cloud metadata (`169.254.0.0/16`, `fd00:ec2::254`), multicast and unspecified addresses.
- **No detours:** HTTP redirects are never followed, proxy environment variables are ignored, and cookies are never kept.
- **Errors say what to do**, for example "192.168.1.20 is on a private network. Ask the operator to add 192.168.1.0/24 to `connections.networks`."

The guarded dialer in `internal/calendar/fetch.go` moves into a shared `internal/egress` (socket S4). Calendar and both connection kinds use it, and the calendar's copy is deleted. The MQTT client is given that dialer for its first connection and every reconnection (autopaho's custom connection hook; to be confirmed when it is built).

## MQTT

```jsonc
{
  "key": "home", "name": "Home broker", "kind": "mqtt", "enabled": true,
  "target": "mqtt://192.168.1.20:1883",                       // mqtt, mqtts, ws, wss (MQTT over WebSocket)
  "credentials": { "username": "papergo", "password": "…" },
  "channels": [
    { "key": "sensors", "name": "Sensors", "topics": "home/+/sensor/#", "uses": ["see"] },
    { "key": "heating", "name": "Heating", "topics": "boiler/#",       "uses": ["see", "control"],
      "access": { "control": { "level": "write", "on": "<Facilities list ID>" } } },
    { "key": "lights",  "name": "Lights",  "topics": "home/+/light/set", "uses": ["control"], "retain": false }
  ]
}
```

### Channels

- `topics` is an MQTT topic filter (`+`, `#`).
- A subscription's filter must lie inside its channel's filter. A publish names one concrete topic that matches it.
- Overlap between filters is decided level by level.
- Filters starting with `$` (including `$share/`, `$SYS/`) are refused.
- **Retain** is a channel option, off by default, so members cannot leave or clear retained state unless the manager allows it.
- Topics are at most 256 bytes. Payloads are at most 256 KiB, or 16 KiB when an app publishes.

### How it runs

- **Client:**
  - Library: `github.com/eclipse/paho.golang` (`autopaho`), MQTT 5 only, used under its EDL-1.0 (BSD-3-Clause) option. Its transitive dependencies are added to [dependencies](../dependencies.md).
  - Brokers: Mosquitto ≥ 1.6, EMQX and HiveMQ speak MQTT 5. Node-RED's embedded Aedes broker and Azure IoT Hub do not, and Test says so plainly.
  - Client ID: `papergo-<connection id>`, with a clean start. PaperGo runs as one process.
- **Always on.** An enabled connection stays connected and subscribed to the filters of its *see* channels, so a dashboard opens with current values. Zigbee2MQTT and Tasmota do not retain telemetry by default. A disabled connection never connects.
- **Hub.** Messages fan out in memory to listeners. Nothing is written to the database.
  - It keeps the **last message of each topic**, bounded to 4 MiB a connection. Payloads over 64 KiB are not kept, and the oldest topics go first.
  - A slow listener drops its oldest messages and is told how many it missed.
  - Messages to an app are **coalesced to the latest per topic**, so a fast topic does not re-render the app for every message.
- **Payloads.** UTF-8 JSON is delivered as JSON, other UTF-8 as text, and anything else as base64 with `"encoding": "base64"`.
- **Publishing.**
  - QoS 0 or 1 (default 1). A publish answers once the broker acknowledges it, within the request timeout.
  - It carries the MQTT 5 user property `papergo-request: <id>`, so receivers can drop repeats.
- **Status:** `connecting`, `connected` or `failed`, with `last_error` and `connected_at`.

## HTTP

For Node-RED `http in` → `http response` flows, and any small HTTP API on the network.

```jsonc
{
  "key": "nodered", "name": "Node-RED", "kind": "http", "enabled": true,
  "target": "http://192.168.1.20:1880/api",                    // http (operator networks only) or https; base URL
  "credentials": { "type": "basic", "username": "papergo", "password": "…" },
  // type: none | basic | bearer {token} | header {name, value}
  "headers": { "X-Site": "home" },                             // fixed, non-secret headers
  "timeout": "10s",                                            // 1–25 s, default 10 s
  "sign": true,                                                // signed caller headers, see below
  "channels": [
    { "key": "heating", "name": "Heating", "path": "/heating/**", "methods": ["GET", "POST"],
      "access": { "control": { "level": "write", "on": "<Facilities list ID>" } } },
    { "key": "status",  "name": "House status", "path": "/status", "methods": ["GET"], "cache": "30s" }
  ]
}
```

Node-RED's `http in` nodes carry no authentication by default. Setting `httpNodeAuth` in its `settings.js` gives them Basic authentication, which is the `basic` credential above. `bearer` and `header` cover API keys and flows that check a header themselves.

### Channels

- **`path`** is a pattern relative to the target's base path, in the syntax `sdk/scope.json` already uses: `*` is one segment, `**` is one or more.
- **`methods`** lists the allowed methods. `GET` and `HEAD` are *see*; `POST`, `PUT`, `PATCH` and `DELETE` are *control*.
- **Overlap:** two channels overlap when they share a use and their patterns can match one path.
- **Path rules:**
  - The path is normalized before it is matched.
  - Dot segments, encoded slashes (`%2F`), backslashes, empty segments, absolute URLs and anything but a path and query are refused.
  - The query string is passed on as given, up to 2 KiB.
- **`cache`** (optional, on `GET` only) keeps a 2xx response for that long and serves it to every caller who passes the channel's *see* access. The endpoint answers the same whoever asks, because it sees only PaperGo's credential. Twenty dashboards polling one flow then cost the Pi one request every 30 seconds. The cache is in memory, at most 4 MiB a connection.

### Requests and responses

A caller sends `{ method, path, headers?, body | body_text | body_base64 }`:
- **Headers** a caller may set: `Accept`, `Content-Type`, `If-Match`, `If-None-Match` and `Idempotency-Key`. Everything else, `Authorization` and `Cookie` first of all, is PaperGo's.
- **Bodies** are at most 1 MiB, or 16 KiB from an app, query included.

PaperGo answers **200 with an envelope**, whatever the endpoint said, so the endpoint's 403 is never taken for PaperGo's:

```jsonc
{ "status": 200, "headers": { "content-type": "application/json", "etag": "\"7\"" }, "body": { "zone": "living", "target": 21 } }
```

- **Body:** JSON is returned parsed, other text as a string, and anything else as `body_base64`, so a Node-RED flow can return a camera snapshot.
- **Size:** responses are at most 4 MiB; a longer one fails rather than being cut short.
- **Headers returned:** only `Content-Type`, `ETag`, `Last-Modified` and `Retry-After`. Never `Set-Cookie`, and never `Location`, which could reveal the target.

PaperGo's own failures are problems:

| Problem | When |
| --- | --- |
| `403 connection_forbidden` / `connection_access_missing` | the caller may not use this channel this way |
| `422 connection_path_invalid` | the path, method or headers are outside the channel or the rules |
| `429 connection_rate_limited` | over the caller's rate (with `Retry-After`) |
| `502 connection_failed` | the endpoint could not be reached, or its TLS was wrong, or it redirected |
| `503 connection_busy` | the connection's concurrency is in use (with `Retry-After`) |
| `504 connection_timeout` | no answer within `timeout` |

### What the endpoint receives

Every request carries:
- the connection's credential and fixed headers;
- `PaperGo-Request-Id`;
- `Idempotency-Key`, when the caller or a workflow operation gives one.

With `sign: true` it also carries the caller and a signature over them and the body:
- `PaperGo-Workspace` and `PaperGo-Subject` (the person or service identity PaperGo decided for);
- `PaperGo-Signature: t=<unix time>,v1=<hex HMAC-SHA256 of "{t}.{subject}.{workspace}.{body}">`, with a per-connection signing secret shown once to managers.

A Node-RED function node can then verify who asked and log it. The timestamp bounds replays: the docs' sample check refuses signatures older than 5 minutes. The planned notification webhooks ([tasks and notifications](tasks-and-notifications.md)) sign `"{timestamp}.{body}"` the same way; both should share one signer when either is built.

### How it runs

- There is no standing connection. Each call goes through `internal/egress` with keep-alive pooling per connection.
- **At most 4 calls at once** a connection; more wait up to 2 seconds, then answer `503 connection_busy`. A Node-RED on a Pi is not flooded.
- **Status:** `ok` or `failing`, from the last call and from Test. Test calls the optional `health_path`, or the base URL.

## Rates and audit

| | MQTT | HTTP |
| --- | --- | --- |
| *Control* rate, per person and channel | 10 a second, bursting to 20 | 10 a second, bursting to 20 |
| *See* rate, per person and connection | — (streams) | 20 a second, bursting to 40; cached answers do not count |
| Audited | every publish: connection, channel, topic, QoS, caller, run ID | every control call: connection, channel, method, path without its query, status, caller, run ID |

Control is always audited, without payloads or bodies, and with no switch to turn it off. PaperGo's control calls are commands at human pace, and "who turned the heating off?" is what audit is for. High-rate machine traffic belongs between Node-RED and the broker, not through PaperGo. *See* is not audited.

Management is audited as `connection.create`, `connection.update` and `connection.delete`.

## API

Management routes live under `/v1/connections`. Routes for callers live under the workspace, by key. That puts the whole management surface on paths that apps and scripts are refused by path.

| Method | Route | Purpose | Needs |
| --- | --- | --- | --- |
| GET / POST | `/v1/connections?workspace={id}` / `/v1/connections` | List with target and status; create `{workspace_id, …}` | manage + `connection.manage` |
| GET / PUT / DELETE | `/v1/connections/{id}` | Read, replace (`If-Match`; credentials and signing secret kept unless given), delete (`If-Match`) | manage + `connection.manage` |
| POST | `/v1/connections/{id}/test` | Connect or call once and report what went wrong | manage + `connection.manage` |
| POST | `/v1/connections/{id}/signing-secret` | Make a new signing secret, shown once | manage + `connection.manage` |
| GET | `/v1/workspaces/{id}/connections` | Key, name, kind, status (`connected`/`ok` or not) and channels, each with what the caller may do; no target or credentials | workspace read |
| GET | `/v1/workspaces/{id}/connections/{key}` | One of them | workspace read |
| POST | `…/connections/{key}/channels/{channel}/publish` | MQTT: `{ topic, payload \| payload_text \| payload_base64, qos?, retain? }`; `payload` is a free-form JSON value, so the generated client sends it | `connection.control` + control access |
| GET | `…/connections/{key}/channels/{channel}/fetch?path=` | HTTP *see*: `GET` the percent-encoded `path` (with its query); `HEAD` with `method=HEAD` | see access |
| POST | `…/connections/{key}/channels/{channel}/call` | HTTP *control*: `{ method, path, headers?, body \| body_text \| body_base64 }` | `connection.control` + control access |

*See* over HTTP is a PaperGo `GET`, so a workflow script treats it as a read: it runs outside the run's transactions, as every read does. It may run again if the run resumes before the read was recorded, which is harmless for a `GET`.

Subscriptions travel on the live stream (S1). An access-checked subscription is added to it with `{ source: "connections", workspace, key, channel, filter }`. The settings monitor is the same subscription, taken with `connection.manage` over every channel.

**Refusals.**
- The app bridge refuses `/v1/connections` and everything under it, for any method.
- `sdk/scope.json` excludes `/v1/connections` and `/v1/connections/**` from scripts.
- Apps and scripts keep the by-key reads, `publish`, `fetch` and `call`.

## Apps

`@papergo/app` gains:

```tsx
// Either kind
const home = useConnection("home");                                          // { kind, connected }

// MQTT
const temp = useTopic("home", "sensors", "home/living/sensor/temperature");  // { payload, at, retained } | undefined
const all  = useTopics("home", "sensors", "home/+/sensor/#");                // Map<topic, message>
await ctx.connections.publish("home", "lights", "home/kitchen/light/set", { on: true });

// HTTP
const state = useFetch("nodered", "status", "/status", { every: 30_000 });   // { data, error, loading, refresh }
const res   = await ctx.connections.fetch("nodered", "heating", "/heating/state?zone=living");
const done  = await ctx.connections.call("nodered", "heating", "POST", "/heating/set", { zone: "living", target: 21 });
// res and done are the envelope: { status, headers, body }; a problem throws RequestError as other requests do
```

- **By key.** Connections are named by key, not chosen with a parameters picker. Keys are stable, readable in source and the same in a copied workspace. A parameter can still hold the key.
- **HTTP and publishing** are `ctx.request` calls to the routes above, which the bridge already forwards. `useFetch` polls at most every 5 seconds, pauses while the page is hidden, and shares one request between components that ask for the same path.
- **Subscribing** is a bridge message, not a route. The app asks the page to subscribe, and the page adds the subscription to its own live stream as the viewer and forwards the frames. It removes them when the app closes. The app never opens a stream itself.
- **History** (charts of the last weeks) comes from items, not from the connection:
  - now, Node-RED writes readings as items through the REST API with an integration token;
  - from phase 2, a workflow trigger with `min_interval` does.

## Workflows

**Control from a run is never done inside the run's write transaction.** This covers MQTT publishes and HTTP calls.
1. When the request comes from a run (`Request.Run`), the route records a `connection.publish` or `connection.call` operation and answers 202 with the operation at `Location` and its ID as `operation_id` in the route's own answer, so a script's typed client reads it: `await ctx.wait(res.operationId)`.
2. The `connections` worker performs it after commit, with the operation ID as `papergo-request` (MQTT) or `Idempotency-Key` (HTTP).
3. The script awaits it with `ctx.wait` like any operation. The operation's result is the envelope, or the problem.

On SQLite this keeps every writer free while the endpoint answers. Delivery is at least once, so commands should be idempotent ("set to 21 °C", not "+1"), and Node-RED flows that change something should honour `Idempotency-Key`. The docs show how.

*See* over HTTP is a plain read (`fetch`), answered at once.

**Triggers on MQTT messages** come in phase 2 (socket S3): `{ type: "connection.message", options: { connection: "home", channel: "sensors", topic: "home/+/sensor/temperature", min_interval: "5m", condition: { path, op, value } } }`. A module's trigger type keeps what it takes in `options`, since trigger definitions are decoded strictly and the engine's own fields belong to its own types.
- They follow the channel's *see* access for the workflow's author.
- `min_interval` and `condition` throttle per topic, so telemetry never becomes a run per message.

HTTP connections have no triggers. A Node-RED flow that wants to start something in PaperGo calls the REST API with an integration token: it can start a workflow's manual run or write items.

## Web app

**Workspace settings → Devices & services** (the API says connections; the UI avoids "connections", which already names AI-assistant connections and relationships):
- the list with kind and live status;
- add or edit: target, CA, credentials, and channels with "Who can see" and "Who can control". For HTTP also: fixed headers, timeout, signing, and per-channel cache;
- **Test**, with errors that say what to do;
- a **monitor**:
  - MQTT: a channel's live messages, with a publish box;
  - HTTP: the last 50 calls, with method, path, status, duration and caller, and a box to try a call.

  Both follow the channel's access.

## Copying a workspace and solutions (phase 2)

Phase 1 leaves connections out of workspace packages, and an export warns that they will need setting up again. An app whose connection key is missing in a copy says "No connection *home* in this workspace".

Phase 2 adds them, through socket S5:
- **Workspace packages** carry key, name, kind, channels and their access. `on` is remapped as a list reference. Target, credentials, fixed headers and signing secret stay behind, and an imported connection is disabled until a target is given.
- **Solutions** declare `parts: { connections: [{ key, kind, settings?, channels }] }` in `solution.json`, where `parts` holds what features add to solutions by the feature's name, with `on` as a slot reference (`$collection:key`), since solutions carry no IDs. Installing creates them disabled and says they need a target; removing or pruning the solution keeps them, as they hold what a manager configured.

## Storage

The feature's own tables in both schema masters; no core table changes:
- `connections`: id, workspace_id, key, name, kind, enabled, config_ciphertext, config_hash, channels JSON, created/updated by and at, version.
  - `kind` is `CHECK IN ('mqtt', 'http')`.
  - The encrypted configuration holds target, CA, credentials, fixed headers and signing secret. `timeout`, `sign` and channels are plain.
- Status, caches and the HTTP call log are in memory.

`kind`, the uses (`see`, `control`) and `level` get named Go types and constants (see "Fixed value sets" in `internal/dal/README.md`).

## Changes outside the module

Per the Extension-First Protocol, `internal/connections` is its own feature, registered in `cmd/api/features.go`: its routes, hub, MQTT client, HTTP caller, worker and tables. Each kind is a provider inside it, behind one interface (validate a channel, check a use, see, control, status, test), so a future kind adds no routes or tables.

What it needs from the rest of PaperGo, each to be approved before it is built:

| # | Change | Kind | Phase | Also serves |
| --- | --- | --- | --- | --- |
| S1 | **Ephemeral module frames on the live stream.** A module registers a subscription source (validate and authorize a subscription; deliver frames to a stream). `/v1/events` carries those frames beside its durable ones. Subscriptions are added and removed by POST against the stream's ID, re-checked every 30 s, and dropped with an `access` frame when access is lost. One stream per tab carries everything, so a dashboard costs no extra stream slot or browser connection. The app bridge gains subscribe and unsubscribe messages. `LIVE_STREAMS_PER_USER` makes the per-user limit of four configurable, for kiosks sharing one account. | socket | 1 | run and operation progress in apps, comments, future modules |
| S4 | **`internal/egress`:** the guarded dialer and HTTP client, with trusted hosts for public addresses, operator networks for the rest, no redirects, no proxies and bounded bodies. Calendar moves onto it. | socket | 1 | calendar, HTTP connections, future fetchers |
| C1 | **Scopes:** `connection.manage` and `connection.control` in `identity.Catalog`; `connection.control` in `LimitableScopes`. | catalog | 1 | — |
| C2 | **Secrets:** `integration.Secrets` takes a purpose for its associated data (`list-integration`, `connection`). The startup guard against a lost deployment key also counts connections. | refactor | 1 | every future encrypted config |
| C3 | **Trusted-host users:** a module reports the hosts it uses, so the usage report and the "still needed" warning cover calendar and connections alike. | socket | 1 | — |
| C4 | **Refusals:** the bridge's refused routes and `sdk/scope.json`. | config | 1 | — |
| S3 | **Module trigger types:** a module registers a trigger type, its schema, matching and event source with the workflow engine. | socket | 2 | inbound mail, webhooks |
| S5 | **Module package parts:** a module contributes what workspace packages and solution manifests carry for it, with reference remapping and secrets left behind. | socket | 2 | list integrations, apps' future parts |

`dms.Service.Authorize` serves the channel check as it is. HTTP adds no change outside the module beyond S4, which phase 1 already builds, and the `'http'` kind in the schema.

## Phases

The whole design above holds from the start. Only its development is phased: phase 1 builds the frame every kind shares, together with MQTT.

1. **MQTT live control.**
   - S1, S4, C1–C4 and `internal/connections` with the provider interface and the MQTT provider.
   - Channels with *see* and *control* access on the workspace or a list; publish with rate limit, audit and workflow operations; settings with Test and monitor.
   - `useConnection`, `useTopic`, `useTopics` and `ctx.connections.publish`.
   - Docs (`docs/connections.md`, apps, identity, configuration, trusted hosts, OpenAPI, SDK).
   - A sample app with README setup steps (Mosquitto, `connections.networks`, channels).
2. **Reacting and travelling:** S3 and the `connection.message` trigger with throttling; S5 for packages and solutions; the sample becomes a solution.
3. **HTTP connections**, as designed above:
   - the HTTP provider and its credentials, signing and cache;
   - the `fetch` and `call` routes and the `connection.call` operation;
   - `useFetch`, `ctx.connections.fetch` and `ctx.connections.call`;
   - the call monitor, and a Node-RED flow in the sample.
4. **Later:** raw WebSocket endpoints (MQTT over WebSocket is in phase 1), item-bound channels, folders and items as `on`, MQTT 3.1.1 if asked for.

## Proposed after review (to confirm)

- *See* defaults to members; widening to visitors is a choice.
- Control is always audited, with no per-channel switch.
- MQTT 5 only for now.
- One workspace per connection. An organisation-level endpoint that several workspaces reference can come later without breaking this.
- HTTP answers are an envelope (200 with the endpoint's status inside), not a pass-through of the endpoint's status.

## Still open

1. The UI name: "Devices & services" covers both kinds. Is it the right one?
2. Kiosks: a shared account on several tablets needs more than four streams. Should `LIVE_STREAMS_PER_USER` default higher than 4?
3. The members default for *see* is `write` on the workspace, so a token needs `item.write` to read sensors or fetch from Node-RED. A read-only dashboard token would need a channel set to `read`. Is that acceptable, or should *see* take a level of its own?
4. Should HTTP signing be on by default for new HTTP connections? It costs Node-RED users a function node to check it, but it is what lets a flow trust who asked.
