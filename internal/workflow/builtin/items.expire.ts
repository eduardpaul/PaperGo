// Every day, unpublishes the published items whose date field is before
// today: the schedule starts one run for each item that meets the
// condition.
import { workflow } from "@papergo/workflow";

interface Params {
  collection: string;
  field: string;
  cron: string;
  time_zone: string;
  content_type?: string;
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "schedule", collection_id: p.collection, content_type_id: p.content_type, cron: p.cron, time_zone: p.time_zone, surface: "published" },
  ],
  condition: (p) => ({ field: p.field, op: "lt", value_ref: "today" }),
  async run(ctx) {
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    await ctx.api.v1.items.byId(ctx.itemId!).unpublish.post({ headers: { "If-Match": `"${item!.version}"` } });
  },
});
