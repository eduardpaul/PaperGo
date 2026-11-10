// When a reading falls below threshold_l, asks the buyers to fill the tank.
// The task's form is the purchase: answering it files the purchase, and a
// purchase filed any other way closes the task. A later low reading updates
// the open task to its level instead of asking again.
import { OperationError, json, workflow } from "@papergo/workflow";
import type { PaperGoClient } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  purchase_type: string;
  threshold_l: number;
  // People, group:<id>, role:owners or role:members.
  buyers: string[];
}

// What diesel-levels returns for a reading it converted.
interface Levels {
  level_liters: string;
  fill_percent: string;
  previous_level_liters: string | null;
}

const KEY = "diesel_refill";

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
    { type: "wf.diesel-levels.completed", collection_id: p.list, content_type_id: p.reading_type },
    { type: "item.created", collection_id: p.list, content_type_id: p.purchase_type },
  ],
  // One run per reading at a time.
  concurrency: "skip",
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;

    // A run that follows diesel-levels carries the reading as its item too, so
    // the event says which this is.
    if (ctx.run.event.type === "item.created") {
      // Someone bought diesel: close the open refill task, whose run then ends.
      const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });
      const bought = ctx.trigger?.kind === "item" ? ctx.trigger.item.name : "diesel";
      for (const task of open?.value ?? []) await close(api, task.id!, `Bought: ${bought}.`);
      return "closed";
    }
    const levels = ctx.run.event.data.outputs as Levels | undefined;
    if (!levels || Number(levels.level_liters) >= p.threshold_l) return "not low";

    const title = `Fill the diesel tank: ${levels.level_liters} L left`;
    const description = `The tank is at ${levels.fill_percent} %, below ${p.threshold_l} L.`;
    let answer;
    try {
      // Unique: while the refill task is open, asking again is refused.
      answer = await ctx.ask<{ bought_liters: number; total_price: number; purchased_at: string; supplier?: string }>({
        key: KEY,
        unique: true,
        title,
        description,
        itemId: ctx.itemId,
        assignees: p.buyers,
        priority: "high",
        inputSchema: {
          type: "object",
          required: ["bought_liters", "total_price"],
          properties: {
            bought_liters: { type: "number", title: "Litres bought", minimum: 0 },
            total_price: { type: "number", title: "Total price", minimum: 0 },
            purchased_at: { type: "string", format: "date-time", title: "Bought at", "x-papergo-default": "now" },
            supplier: { type: "string", title: "Supplier", maxLength: 200 },
          },
        },
        outcomes: [{ key: "bought", label: "Save purchase", style: "primary" }],
      });
    } catch (e) {
      const problem = e as { code?: string; openTaskId?: string };
      if (problem.code === "task_open") {
        // Already asked: the open task shows this reading's level instead.
        const task = api.v1.tasks.byId(problem.openTaskId!);
        const current = await task.get();
        try {
          await task.patch({ title, description }, { headers: { "If-Match": `"${current!.version}"` } });
        } catch (err) {
          // Answered (409), or changed by another reading (412), meanwhile.
          const status = (err as { responseStatusCode?: number }).responseStatusCode;
          if (status !== 409 && status !== 412) throw err;
        }
        return "updated";
      }
      if (e instanceof OperationError) return "bought elsewhere";
      throw e;
    }

    const input = answer.input!;
    const liters = Number(input.bought_liters).toFixed(1);
    await api.v1.resources.byId(p.list).children.post({
      kind: "item",
      name: `Purchase ${input.purchased_at.slice(0, 10)} · ${liters} L`,
      contentTypeId: p.purchase_type as never,
      values: json({
        diesel_purchased_at: input.purchased_at,
        diesel_bought_liters: liters,
        diesel_total_price: Number(input.total_price),
        ...(input.supplier ? { diesel_supplier: input.supplier } : {}),
      }),
    });
    return "bought";
  },
});
