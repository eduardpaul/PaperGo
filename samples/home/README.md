# Home

A house on one screen: each room's temperature as its sensor reports it on an MQTT broker, its light and fan, and the heating that a Node-RED flow runs, with a boost. A workflow turns a room's fan on when the room gets too warm. It is a [solution](../../docs/solutions.md) that shows [connections](../../docs/connections.md): its app and workflow reach the broker and Node-RED by key, through channels, and never see an address or a credential.

It installs:
- the **Facilities** list, whose editors are the people who may boost the heating;
- two connections, created disabled and without an address:
  - **Home broker** (`broker`, MQTT) with the channels **Sensors** (`home/+/sensor/#`, see), **Lights** (`home/+/light/set`, control) and **Fans** (`home/+/fan/set`, control), each open to the workspace's members;
  - **Node-RED** (`nodered`, HTTP, signed) with **House status** (`GET /status`, cached 30 seconds, for members) and **Heating** (`POST /heating/**`, for those who can edit **Facilities**);
- the **Home** app, the dashboard;
- the **Too warm** workflow.

Sensors publish `home/<room>/sensor/temperature` with `{"c": 21.5}`; lights and fans take `{"state": "ON"}`, `"OFF"` or `"TOGGLE"` on `home/<room>/light/set` and `home/<room>/fan/set`, as Zigbee2MQTT and Tasmota devices do once renamed to these topics.

## Set up

1. **Let PaperGo reach the house's network.** The operator lists the network in `config.yaml`, or `CONNECTION_NETWORKS`, and restarts PaperGo:

   ```yaml
   connections:
     networks: [192.168.1.0/24]
   ```

   For containers on one Compose network, list that network, such as `172.18.0.0/16`. A broker or a Node-RED on the internet needs TLS and a [trusted host](../../docs/trusted-hosts.md) instead.

2. **Give PaperGo its own broker user**, limited to the sample's topics. With Mosquitto:

   ```sh
   mosquitto_passwd /mosquitto/config/passwd papergo
   ```

   and in its ACL file (`acl_file` in `mosquitto.conf`):

   ```text
   user papergo
   topic read home/+/sensor/#
   topic write home/+/light/set
   topic write home/+/fan/set
   ```

   PaperGo speaks MQTT 5: Mosquitto 1.6 or later, EMQX and HiveMQ do; Node-RED's own Aedes broker does not.

3. **Import the Node-RED flow.** In Node-RED, **Import** `nodered/flows.json`. It adds the **PaperGo home** tab: `GET /status` and `POST /heating/boost`, which refuses a call PaperGo did not sign. Turn on Basic authentication for HTTP nodes in Node-RED's `settings.js`:

   ```js
   httpNodeAuth: { user: "papergo", pass: "<bcrypt hash of the password>" },
   ```

   (`node-red admin hash-pw` makes the hash.)

4. **Apply the solution** to a workspace, as someone who manages it and has `solution.manage` and `connection.manage`:

   ```sh
   papergo solution apply ./samples/home --workspace Home
   ```

   It says which connections need their address.

5. **Give each connection its address and credentials.** In **Workspace settings → Devices & services**:
   - **Home broker:** address `mqtt://192.168.1.20:1883`, user name `papergo` and its password; enable it and choose **Test**.
   - **Node-RED:** address `http://192.168.1.20:1880`, user name and password of `httpNodeAuth`; enable it, choose **New signing secret**, and give Node-RED the secret as the environment variable `PAPERGO_SIGNING_SECRET` (for the Docker image, `-e PAPERGO_SIGNING_SECRET=…`), then restart it. **Test** calls `/status`.

6. **Choose who may boost the heating:** open the **Facilities** list's permissions, break its inheritance and grant `write` to those people or their group. Workspace owners always may.

Open **Home** from the sidebar. A room appears once its sensor reports. To try it without devices:

```sh
mosquitto_pub -h 192.168.1.20 -u papergo -P … -t home/living/sensor/temperature -m '{"c": 27.5}'
```

The PaperGo user may only read sensors, so publish readings as a device's user, or as an administrator of the broker.

## The workflow

| Workflow | Starts on | Does |
| --- | --- | --- |
| **Too warm** | a `home/<room>/sensor/temperature` reading above `too_warm_c` (26 °C by default), at most every 15 minutes a room | Publishes `{"state": "ON"}` to `home/<room>/fan/set` as an operation it waits for, and logs it. |

It acts as whoever applied the solution, who must be allowed to see **Sensors** and control **Fans**. Change the threshold with the solution's `too_warm_c` setting.

## What it shows

- **Connections by key.** The app calls `useTopics("broker", "sensors", "home/+/sensor/temperature")`, `ctx.connections.publish("broker", "lights", …)`, `useFetch("nodered", "status", "/status")` and `ctx.connections.call("nodered", "heating", "POST", "/heating/boost", …)`. A copy of the workspace, or the solution installed elsewhere, has the same keys and needs only its addresses.
- **Borrowed access.** Boosting the heating needs `write` on **Facilities**, managed in its Permissions dialog; the rest is open to members.
- **Signed calls.** Node-RED checks `PaperGo-Signature`, and logs who asked from `PaperGo-Subject`.
- **Throttled triggers.** A sensor that reports every few seconds starts at most one run a room every 15 minutes.
