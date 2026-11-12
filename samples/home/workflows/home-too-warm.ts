// When a room's temperature, home/<room>/sensor/temperature with a
// payload such as {"c": 27.5}, rises above too_warm_c, turns the room's fan
// on through the fans channel. The trigger starts at most one run a room
// every 15 minutes, so a sensor reporting every few seconds does not become
// a run per reading. The publish is an operation the run waits for: it is
// sent once the run's step is saved, at least once, so it sets the fan on
// rather than toggling it.
import { json, workflow } from "@papergo/workflow";

interface Params {
  too_warm_c: number;
}

interface Reading {
  topic: string;
  payload: { c: number };
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    {
      type: "connection.message",
      options: {
        connection: "broker",
        channel: "sensors",
        topic: "home/+/sensor/temperature",
        min_interval: "15m",
        condition: { path: "c", op: "gt", value: p.too_warm_c },
      },
    },
  ],
  async run(ctx) {
    const { topic, payload } = ctx.run.event.data as unknown as Reading;
    const room = topic.split("/")[1];
    const fans = ctx.api.v1.workspaces.byId(ctx.run.workspace_id).connections.byKey("broker").channels.byChannel("fans");
    const published = await fans.publish.post({ topic: `home/${room}/fan/set`, payload: json({ state: "ON" }) });
    await ctx.wait(published!.operationId!);
    ctx.log(`${room} is ${payload.c} °C: its fan is on`);
    return { room, celsius: payload.c };
  },
});
