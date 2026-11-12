# Connections

A **connection** is an outside endpoint of a workspace, which the workspace's [apps](apps.md) and [workflows](workflows.md) reach through PaperGo. It is either an **MQTT** broker, such as Mosquitto beside Zigbee2MQTT, or an **HTTP** service, such as a Node-RED flow (`http in` → `http response`).

PaperGo holds the connection itself. Apps and workflows name it by **key** and never see its address or credentials, and PaperGo checks every use before anything leaves. An app's frame still has no network of its own: an app reaches outside PaperGo only through channels a workspace manager opened, as people who may use them.

The web app calls them **Devices & services**, under **Workspace settings**. The [Home sample](../samples/home/README.md) is a solution that sets up a broker and a Node-RED flow end to end.

## A connection

| Field | Meaning |
| --- | --- |
| `key` | What apps and workflows name it by; from the name unless given; unique in the workspace; never changes |
| `name`, `kind`, `enabled` | `kind` is `mqtt` or `http`. A disabled connection never connects |
| `target` | Where it goes. MQTT: `mqtt://`, `mqtts://`, `ws://` or `wss://` (MQTT over WebSocket). HTTP: the base URL, `http://` or `https://` |
| `tls.ca` | PEM certificates trusted beside the system's, for a self-signed certificate on the local network |
| `credentials` | Write-only, kept encrypted. MQTT: `none` or `basic` (user name and password). HTTP: `none`, `basic`, `bearer` (`token`) or `header` (`name` and `value`) |
| `channels` | What callers may reach, and who may; up to 50 |

HTTP connections also have:
- `headers`: fixed, non-secret headers;
- `timeout`: 1 to 25 seconds, 10 by default;
- `sign`: send the caller with a signature (below);
- `health_path`: what **Test** calls, the base URL by default.

The target, CA, credentials, fixed headers and signing secret are encrypted with the deployment's integration key (`<database>.integrations.key`). Once a list integration or a connection is stored, a missing key stops the server from starting rather than making a new one that opens nothing.

## Channels

A **channel** is a named, bounded part of what the connection reaches:
- **MQTT:** a topic filter (`topics`, with `+` for one level and `#` for the last levels) and its `uses`, `see` or `control` or both. `retain` lets publishes be retained; it is off by default.
- **HTTP:** a path pattern relative to the target's base (`path`, with `*` for one segment and a last `**` for one or more) and its `methods`. `GET` and `HEAD` are *see*, `POST`, `PUT`, `PATCH` and `DELETE` are *control*. `cache` keeps 2xx `GET` answers for 1 second to 1 hour.

Every use is either **see** or **control**: subscribing and reading are seeing, publishing and changing are controlling.

Callers name channels, never addresses, and whatever is outside every channel is unreachable through PaperGo, whatever the endpoint itself allows. Two channels may not cover the same topic or path for the same use: `home/+/sensor/#` and `home/boiler/#` both cover `home/boiler/sensor/temperature`, so they cannot both *see*. Saving refuses this and names both, because otherwise a wide channel would open a narrow, restricted one. Topics starting with `$` (the broker's own, and shared subscriptions) are refused.

```jsonc
{
  "workspace_id": "…", "kind": "mqtt", "name": "Home broker",
  "target": "mqtt://192.168.1.20:1883",
  "credentials": { "username": "papergo", "password": "…" },
  "channels": [
    { "key": "sensors", "name": "Sensors", "topics": "home/+/sensor/#", "uses": ["see"] },
    { "key": "lights",  "name": "Lights",  "topics": "home/+/light/set", "uses": ["control"] },
    { "key": "heating", "name": "Heating", "topics": "boiler/#", "uses": ["see", "control"],
      "access": { "control": { "level": "write", "on": "<Facilities list ID>" } } }
  ]
}
```

## Who may do what

A connection keeps no permissions of its own: each channel borrows them from the workspace or from one of its lists.

| Action | Needs |
| --- | --- |
| Create, change, test, monitor and delete connections; see their targets and status | manage access to the workspace and the `connection.manage` scope |
| List the workspace's connections by key, without targets or credentials | read access to the workspace |
| See through a channel (subscribe, `GET`) | the channel's *see* access |
| Control through a channel (publish, change) | the `connection.control` scope and the channel's *control* access |

A channel has an `access` for each use, `{ level, on }`: `level` is `read`, `write` or `manage`, and `on` is `workspace` (the default) or a list or library of the workspace. Without one, a use is open to the workspace's members and owners (`write` on the workspace). *See* defaults to members, not visitors: what a house reports tells when nobody is home.

To let only Facilities drive the boiler, make a Facilities list, break its inheritance, grant the Facilities group `write` on it, and choose it for the heating channel's control. Groups, nested and OIDC groups and workspace roles then all apply, managed in the list's Permissions dialog and evaluated on every use.

- **Workspace owners can always see and control**, and managers of connections see every channel in the monitor.
- **An app acts as its viewer, a workflow as its author, and a token as its holder**, within the token's scopes. A token limited to one list may use a channel borrowed from that list, and not one borrowed from the workspace.
- If the list a channel borrows from is deleted, its use is refused with `403 connection_access_missing` until it is restored or a manager chooses another. Saving checks that the manager can read the list chosen.
- The Member role grants `connection.control`, and limited tokens may hold it. It does not grant `connection.manage`, which administrators have: a connection reaches the server's network for everyone who may use it.

The endpoint sees PaperGo's credential, not who asked; PaperGo has decided access before the request leaves. An HTTP connection can pass the caller on in signed headers.

## Network

Connections reach what the operator allows, checked on the address each connection, reconnection or call dials:
- **Private addresses** (RFC 1918, ULA, loopback, `100.64.0.0/10` and the other non-public ranges) only inside the networks the operator lists in [`connections.networks`](configuration.md) (`CONNECTION_NETWORKS`), such as `192.168.1.0/24`. There, plain `mqtt`, `ws` and `http` are allowed.
- **Public addresses** only over TLS (`mqtts`, `wss`, `https`) and on a [trusted host](trusted-hosts.md).
- **Never:** link-local and cloud metadata addresses, multicast and unspecified addresses.
- Redirects are never followed, proxy settings are ignored and cookies are never kept. A host that resolves to any address it may not reach is refused whole.

**Test** says what to do, for example "192.168.1.20 is on a private network; ask the operator to add 192.168.1.0/24 to connections.networks."

## MQTT

PaperGo speaks MQTT 5: Mosquitto 1.6 or later, EMQX and HiveMQ do; Node-RED's embedded Aedes broker does not, and Test says so.

- An enabled connection stays connected, with the client ID `papergo-<connection id>` and a clean session, and subscribed (QoS 1) to the topics of its *see* channels.
- PaperGo keeps the last message of each topic in memory, up to 4 MiB a connection and 64 KiB a message, so a dashboard opens with current values. Nothing is written to the database.
- A subscription hears the topics its filter matches, which lies inside its channel's topics: current values first, then each new message. A slow subscriber gets the latest message of each topic rather than every one.
- Payloads that are UTF-8 JSON arrive as JSON, other UTF-8 as a string, and anything else in base64 with `"encoding": "base64"`.
- A publish names one topic inside its channel and its message: `payload`, a JSON value (a string is sent as its text), `payload_text` such as `ON`, or `payload_base64`. It takes QoS 0 or 1 (1 by default, answered once the broker acknowledges it), and `retain` only where the channel allows it. It carries the MQTT 5 user property `papergo-request`, so receivers can drop repeats. Payloads are at most 256 KiB, or 16 KiB from an app.
- Status is `connecting`, `connected` or `failed`, with the last error.

## HTTP

There is no standing connection: each call goes out through the guarded dialer, with keep-alive, at most 4 calls at once a connection. A fifth waits up to 2 seconds, then answers `503 connection_busy`.

A caller sends `{ method, path, headers?, body? }`:
- `path` lies inside the channel's pattern, with its query (at most 2 KiB). Dot segments, encoded slashes, backslashes, empty segments and absolute URLs are refused.
- A caller sets only `Accept`, `Content-Type`, `If-Match`, `If-None-Match` and `Idempotency-Key`; the rest, `Authorization` and `Cookie` first of all, is PaperGo's.
- A JSON `body` is sent as JSON, or as its text when it is a string and `Content-Type` is not JSON; `body_text` sends text as it is and `body_base64` sends bytes. Bodies are at most 1 MiB, or 16 KiB from an app.

PaperGo answers 200 with the endpoint's answer in an envelope, whatever the endpoint said, so its 403 is never taken for PaperGo's:

```json
{ "status": 200, "headers": { "content-type": "application/json", "etag": "\"7\"" }, "body": { "zone": "living", "target": 21 } }
```

- JSON bodies come parsed, other text as a string, anything else as `body_base64`. Answers over 4 MiB fail.
- Only `Content-Type`, `ETag`, `Last-Modified` and `Retry-After` come back; never `Set-Cookie` or `Location`.
- A channel's `cache` serves a fresh `GET` answer to every caller with *see* access: the endpoint answers the same whoever asks, since it sees only PaperGo's credential.

PaperGo's own failures are problems:

| Problem | When |
| --- | --- |
| `403 connection_forbidden`, `connection_access_missing` | the caller may not use the channel this way |
| `422 connection_path_invalid` | the path, method or headers are outside the channel or the rules |
| `429 connection_rate_limited` | over the caller's rate, with `Retry-After` |
| `502 connection_failed` | the endpoint could not be reached, its TLS was wrong, or it redirected |
| `503 connection_busy`, `connection_disabled` | four calls already, not connected yet, or disabled; with `Retry-After` when it is worth waiting |
| `504 connection_timeout` | no answer within the timeout |

Why an endpoint could not be reached names its address or its certificate, so `connection_failed` does not say: the connection's managers read it in its status and its monitor.

Status is `ok` or `failing` from the last call, and `unknown` before one.

### What the endpoint receives

Every call carries the connection's credential and fixed headers, `PaperGo-Request-Id`, and the caller's or the workflow operation's `Idempotency-Key` when there is one. With `sign` on it also carries:
- `PaperGo-Workspace` and `PaperGo-Subject`, the person or service identity PaperGo decided for;
- `PaperGo-Signature: t=<unix time>,v1=<hex HMAC-SHA256 of "{t}.{subject}.{workspace}.{body}">`, keyed with the connection's signing secret.

The signing secret is made when an HTTP connection is created, and anew with **New signing secret**; each is shown once. A Node-RED function node checks it before its flow acts:

```js
const crypto = global.get("crypto"); // functionGlobalContext: { crypto: require("crypto") } in settings.js
const secret = env.get("PAPERGO_SIGNING_SECRET");
const [t, v1] = (msg.req.headers["papergo-signature"] || "").replace("t=", "").split(",v1=");
const signed = `${t}.${msg.req.headers["papergo-subject"]}.${msg.req.headers["papergo-workspace"]}.${msg.req.rawBody ?? ""}`;
const expected = crypto.createHmac("sha256", secret).update(signed).digest("hex");
if (!v1 || v1.length !== expected.length || !crypto.timingSafeEqual(Buffer.from(v1), Buffer.from(expected)) || Math.abs(Date.now() / 1000 - Number(t)) > 300) {
  msg.statusCode = 401;
  return [null, msg]; // to an http response node
}
return [msg, null];
```

Node-RED's `http in` nodes carry no authentication by default; setting `httpNodeAuth` in `settings.js` gives them Basic authentication, which is the `basic` credential. A flow that changes something should honour `Idempotency-Key`: a workflow's call can arrive twice.

## Rates and audit

| | MQTT | HTTP |
| --- | --- | --- |
| *Control*, per person and channel | 10 a second, bursting to 20 | 10 a second, bursting to 20 |
| *See*, per person and connection | (streams) | 20 a second, bursting to 40; cached answers do not count |
| Audited | every publish: channel, topic, QoS, retain, caller, request and run | every control call: channel, method, path without its query, status or error, caller, request and run |

Control is always audited, without payloads or bodies. Seeing is not. Managing is audited as `connection.create`, `connection.update` and `connection.delete`.

## Apps

`@papergo/app` names connections by key:

```tsx
import { app, useConnection, useTopic, useTopics, useFetch, Card, Stat } from "@papergo/app";

export default app({
  render(ctx) {
    const home = useConnection("home");                                          // { kind, connected, connection }
    const temp = useTopic("home", "sensors", "home/living/sensor/temperature");  // { payload, at, retained } or undefined
    const all = useTopics("home", "sensors");                                    // Map<topic, message>
    const status = useFetch("nodered", "status", "/status", { every: 30_000 });  // { data, error, loading, refresh }
    const toggle = () => ctx.connections.publish("home", "lights", "home/kitchen/light/set", { state: "TOGGLE" });
    const warmer = () => ctx.connections.call("nodered", "heating", "POST", "/heating/set", { zone: "living", target: 21 });
    return <Card title="Home"><Stat label="Living room" value={String(temp?.payload ?? "–")} /></Card>;
  },
});
```

- `ctx.connections.publish`, `fetch` and `call` are requests to the routes below, sent by the page as the viewer; a refusal throws a `RequestError`. `fetch` and `call` answer the envelope.
- `useTopic` and `useTopics` ask the page to subscribe its own live stream as the viewer, to the app's workspace only, and the page forwards what it hears. The app never opens a stream, and its subscriptions end when it closes.
- `useFetch` reads at most every 5 seconds, pauses while the page is hidden, and shares one request between components that ask for the same path.
- `useConnection` reads whether a connection works every 30 seconds.
- History, such as a chart of the last weeks, comes from items, not from the connection: have Node-RED write readings as items through the REST API with an integration token.

## Workflows

A workflow script uses connections through `ctx.api.v1.workspaces.byId(id).connections`, as its author.

- **Control from a run is never done inside the run's write transaction.** A run's publish or call is accepted as a `connection.publish` or `connection.call` operation (202) and performed after the step commits, with the operation's ID as `papergo-request` or `Idempotency-Key`. The script awaits it with `ctx.wait`; its result is what the publish or the call answers. Delivery is at least once, so commands should set a value ("set to 21 °C") rather than change it ("+1").
- **Publishing and calling** from a script use `json()` for a JSON payload or body, and the answer names the operation: `await ctx.wait(published!.operationId!)`.
- **Seeing over HTTP** is `fetch`, a GET, which a script reads like any other read, outside the run's transactions. A run's `call` with GET or HEAD is refused with 422.
- The management routes are refused to scripts, and to apps.

### Messages start runs

A `connection.message` trigger starts a run for an MQTT message:

```ts
import { json, workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{
    type: "connection.message",
    options: { connection: "home", channel: "sensors", topic: "home/+/sensor/temperature", min_interval: "15m", condition: { path: "c", op: "gt", value: 25 } },
  }],
  async run(ctx) {
    const { topic, payload } = ctx.run.event.data as { topic: string; payload: { c: number } };
    const room = topic.split("/")[1];
    const published = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).connections.byKey("home").channels.byChannel("fans").publish.post({ topic: `home/${room}/fan/set`, payload: json({ on: true }) });
    await ctx.wait(published!.operationId!);
  },
});
```

- `connection` and `channel` are keys. The channel must *see*, and the workflow's author must be allowed to see it, when the workflow is saved and again for each run.
- `topic` is a filter inside the channel's topics; the channel's own by default.
- `min_interval` starts at most one run per topic that often, from 1 second to 24 hours, 1 minute by default, so telemetry never becomes a run per message.
- `condition` tests the payload, or the value at `path` (dot-separated keys) of a JSON payload: `op` is `eq`, `ne`, `gt`, `gte`, `lt` or `lte` against `value`, or `changed`, which holds when the value differs from the last one that started a run for the topic, so a change `min_interval` holds back starts one once it has passed. A payload it cannot read does not start a run.
- Retained messages, which the broker sends again on every reconnection, start nothing.
- The run's event is `connection.message` with `data` holding `connection`, `channel`, `topic`, `payload` (as subscriptions deliver it, with `encoding` for bytes; left out above 16 KiB, with `payload_omitted`) and `at`.
- A saved or published workflow's triggers take effect within 30 seconds. The throttles are kept in memory, so a restart may start one more run per topic.

HTTP services have no triggers: a Node-RED flow that wants to start something calls the REST API with an integration token, to start a workflow's manual run or write items.

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET, POST | `/v1/connections?workspace={id}`, `/v1/connections` | List with targets and status; create `{workspace_id, kind, …}` |
| GET, PUT, DELETE | `/v1/connections/{id}` | Read, replace (`If-Match`; credentials and signing secret kept unless given), delete (`If-Match`) |
| POST | `/v1/connections/{id}/test` | Connect or call once and say what went wrong |
| POST | `/v1/connections/{id}/signing-secret` | Make a new signing secret, shown once |
| GET | `/v1/connections/{id}/calls` | An HTTP connection's last 50 calls |
| GET | `/v1/workspaces/{id}/connections`, `…/connections/{key}` | Key, name, kind, whether it is connected, and each channel with what the caller may do |
| POST | `…/connections/{key}/channels/{channel}/publish` | MQTT: `{ topic, payload \| payload_text \| payload_base64, qos?, retain? }` |
| GET | `…/connections/{key}/channels/{channel}/fetch?path=` | HTTP *see*: `GET`, or `HEAD` with `method=HEAD` |
| POST | `…/connections/{key}/channels/{channel}/call` | HTTP: `{ method, path, headers?, body \| body_text \| body_base64 }` |

Subscriptions travel on the [live stream](workflows.md): its `connected` frame names the stream's ID, and `POST /v1/events/{stream}/subscriptions` with `{ "source": "connections", "workspace", "key", "channel", "filter"? }` adds one, answering its `id`. Its frames are `connection.messages`, `{ subscription, connection, channel, messages, missed? }`. The stream checks each subscription's access every 30 seconds and ends one whose access was lost with an `access` frame; `DELETE /v1/events/{stream}/subscriptions/{id}` ends one sooner. A stream holds at most 50 subscriptions, and they end with it, so a client subscribes again on each stream it opens.

A person holds four live streams at once unless the operator sets [`server.live_streams_per_user`](configuration.md), as for kiosks that share one account.

## Copying a workspace

[Workspace packages](workspace-portability.md) carry a connection's key, name, kind, settings and channels, with each channel's access remapped to the copy's lists. Its address, CA, credentials, fixed headers and signing secret stay behind: an imported connection is disabled and has no address, and the import's warnings name each one to set up. An HTTP connection gets a new signing secret, shown with **New signing secret**. An organization package leaves out the connections of workspaces in the recycle bin, and its warnings name them. [Solutions](solutions.md#connections) declare connections the same way.
