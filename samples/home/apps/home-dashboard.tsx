// The house at a glance: each room's temperature as its sensor reports it
// on the broker, current values first, its light and fan, and the heating
// as Node-RED reports it, with a boost. The app reaches the broker and
// Node-RED only through the workspace's connections, by key: it never sees
// their addresses or credentials, and PaperGo checks what the viewer may
// see and control. Readings: home/<room>/sensor/temperature with
// {"c": 21.5}; lights and fans take {"state": "ON" | "OFF" | "TOGGLE"}, as
// Zigbee2MQTT and Tasmota do.
import { useState } from "react";
import { app, Button, Card, Grid, Notice, Stat, RequestError, useApp, useConnection, useFetch, useTopics, type TopicMessage } from "@papergo/app";

interface Status {
  heating?: { mode?: string; target_c?: number; boost_until?: string };
}

function celsius(m: TopicMessage | undefined): number | undefined {
  const c = (m?.payload as { c?: unknown } | undefined)?.c;
  return typeof c === "number" ? c : undefined;
}

function refused(e: unknown): string {
  return e instanceof RequestError ? e.detail ?? e.message : String(e);
}

function Room({ room, reading }: { room: string; reading?: TopicMessage }) {
  const ctx = useApp();
  const [problem, setProblem] = useState<string>();
  const send = async (channel: "lights" | "fans", device: "light" | "fan") => {
    setProblem(undefined);
    try {
      await ctx.connections.publish("broker", channel, `home/${room}/${device}/set`, { state: "TOGGLE" });
    } catch (e) {
      setProblem(refused(e));
    }
  };
  const c = celsius(reading);
  return (
    <Card
      title={room}
      actions={
        <>
          <Button onClick={() => void send("lights", "light")}>Light</Button>
          <Button onClick={() => void send("fans", "fan")}>Fan</Button>
        </>
      }
    >
      <Stat label="Temperature" value={c === undefined ? "–" : `${ctx.format.decimal(String(c), 1)} °C`} hint={reading ? ctx.format.relative(reading.at) : "No reading yet"} />
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Card>
  );
}

function Heating() {
  const ctx = useApp();
  const status = useFetch<Status>("nodered", "status", "/status", { every: 30_000 });
  const [problem, setProblem] = useState<string>();
  const heating = status.data?.body?.heating;
  const boost = async () => {
    setProblem(undefined);
    try {
      const answer = await ctx.connections.call("nodered", "heating", "POST", "/heating/boost", { minutes: 30 });
      if (answer.status >= 400) setProblem(`Node-RED answered ${answer.status}`);
      status.refresh();
    } catch (e) {
      setProblem(refused(e));
    }
  };
  return (
    <Card
      title="Heating"
      actions={
        <Button variant="primary" onClick={() => void boost()}>
          Boost 30 minutes
        </Button>
      }
    >
      {status.error && <Notice tone="warning">{refused(status.error)}</Notice>}
      <Stat label="Mode" value={heating?.mode ?? "–"} hint={heating?.target_c === undefined ? undefined : `Target ${heating.target_c} °C`} />
      {heating?.boost_until && <Stat label="Boost until" value={ctx.format.dateTime(heating.boost_until)} />}
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Card>
  );
}

function Dashboard() {
  const broker = useConnection("broker");
  const readings = useTopics("broker", "sensors", "home/+/sensor/temperature");
  const rooms = [...readings.keys()].map((topic) => topic.split("/")[1]).sort();
  return (
    <>
      {broker.error && <Notice tone="warning">{refused(broker.error)}</Notice>}
      {broker.connection && !broker.connected && <Notice tone="warning">The home broker is not connected. A manager gives it its address in Workspace settings → Devices & services.</Notice>}
      <Grid>
        {rooms.length === 0 && <Card title="Rooms">No room has reported a temperature yet.</Card>}
        {rooms.map((room) => (
          <Room key={room} room={room} reading={readings.get(`home/${room}/sensor/temperature`)} />
        ))}
        <Heating />
      </Grid>
    </>
  );
}

export default app({ render: () => <Dashboard /> });
