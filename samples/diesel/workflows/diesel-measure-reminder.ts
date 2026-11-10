// Every morning, asks someone to measure the tank when the last reading is
// older than every_days, and owners can ask at once. The task's form is the
// reading: answering it files the reading, and a reading filed any other
// way closes the task. The task stays open until then; nothing asks twice.
import { OperationError, json, workflow } from "@papergo/workflow";
import type { PaperGoClient, models } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  every_days: number;
  // People, group:<id>, role:owners or role:members.
  assignees: string[];
  time_zone: string;
  stick_length_cm: number;
}

const KEY = "diesel_measure";
const DAY = 24 * 3600 * 1000;

// close cancels an open task for reason. A task answered or cancelled
// meanwhile is no longer open (409), which is what close wants anyway.
async function close(api: PaperGoClient, id: string, reason: string): Promise<void> {
  try {
    await api.v1.tasks.byId(id).cancel.post({ reason });
  } catch (e) {
    if ((e as { responseStatusCode?: number }).responseStatusCode !== 409) throw e;
  }
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "schedule", cron: "0 8 * * *", time_zone: p.time_zone },
    { type: "item.created", collection_id: p.list, content_type_id: p.reading_type },
    // Ask now, whenever the last reading is.
    { type: "manual", access: "manage" },
  ],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;

    if (ctx.trigger?.kind === "item") {
      // Someone measured: close the open reminder, whose run then ends.
      const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });
      for (const task of open?.value ?? []) await close(api, task.id!, `Measured: ${ctx.trigger.item.name}.`);
      return "closed";
    }

    const query = { query: { contentTypeId: p.reading_type as never, sort: [{ field: "diesel_measured_at", direction: "desc" as never }] }, select: ["diesel_measured_at"] };
    const last = (await api.v1.resources.byId(p.list).query.post(query, { queryParameters: { top: 1 } }))?.value?.[0] as models.Resource | undefined;
    const at = last?.values?.additionalData?.diesel_measured_at;
    const now = new Date();
    if (ctx.trigger?.kind !== "manual" && at && now.getTime() - new Date(String(at)).getTime() < p.every_days * DAY) return "not due";

    let answer;
    try {
      // Unique: while the last reminder is open, asking again is refused.
      answer = await ctx.ask<{ stick_cm: number; measured_at: string }>({
        key: KEY,
        unique: true,
        title: "Measure the diesel tank",
        description: at ? `The last reading was on ${String(at).slice(0, 10)}.` : "There is no reading yet.",
        assignees: p.assignees,
        priority: "normal",
        dueAt: new Date(now.getTime() + 2 * DAY),
        inputSchema: {
          type: "object",
          required: ["stick_cm"],
          properties: {
            stick_cm: { type: "number", title: "Stick reading from the mouth (cm)", minimum: 0, maximum: p.stick_length_cm },
            measured_at: { type: "string", format: "date-time", title: "Measured at", "x-papergo-default": "now" },
          },
        },
        outcomes: [{ key: "measured", label: "Save reading", style: "primary" }],
      });
    } catch (e) {
      if ((e as { code?: string }).code === "task_open") return "already asked";
      if (e instanceof OperationError) return "measured elsewhere";
      throw e;
    }

    const input = answer.input!;
    const stick = Number(input.stick_cm).toFixed(1);
    await api.v1.resources.byId(p.list).children.post({
      kind: "item",
      name: `Reading ${input.measured_at.slice(0, 10)} · ${stick} cm`,
      contentTypeId: p.reading_type as never,
      values: json({ diesel_measured_at: input.measured_at, diesel_stick_cm: stick }),
    });
    return "measured";
  },
});
