// Keeps items and folders searchable: on every change, permission change
// and term relabel, move or merge, re-reads the entry and writes it to the
// search index.
import { workflow } from "@papergo/workflow";

interface Params {
  collection: string;
}

const entries = ["created", "updated", "deleted", "restored", "purged"];

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    ...["item.archived", "item.unarchived", "item.published", "item.unpublished", ...entries.map((e) => `item.${e}`), ...entries.map((e) => `folder.${e}`)].map((type) => ({ type, collection_id: p.collection })),
    { type: "permissions.changed", collection_id: p.collection, actions: ["replace", "move"] },
    { type: "term.changed" },
    { type: "term.merged" },
    { type: "term.moved" },
    { type: "manual", collection_id: p.collection, selection: "per_item" },
  ],
  concurrency: "coalesce",
  async run(ctx) {
    const { event, workspace_id: workspace } = ctx.run;
    const sync = ctx.api.v1.workspaces.byId(workspace).search.sync;
    if (!event.type.startsWith("term.")) {
      const id = event.resource_id ?? ctx.itemId;
      if (id) await sync.post({ resourceIds: [id] });
      return;
    }
    // A relabel, move or merge changes the words and keywords of every entry
    // holding the term, or below it where values apply their parents.
    const data = event.data;
    const terms: string[] = [];
    if (event.type === "term.changed") {
      const changed = (data.changed_fields as string[] | undefined) ?? [];
      if (changed.some((f) => f === "name" || f === "labels" || f === "synonyms")) terms.push(data.term_id as string);
    } else if (event.type === "term.moved") {
      terms.push(data.term_id as string);
    } else {
      terms.push(data.source_term_id as string, data.target_term_id as string);
    }
    if (terms.length === 0) return;
    let after: string | undefined;
    do {
      const page = await ctx.api.v1.workspaces.byId(workspace).search.termEntries.get({
        queryParameters: { termId: terms, collectionId: ctx.params.collection, after },
      });
      if (page?.items?.length) await sync.post({ resourceIds: page.items });
      after = page?.next ?? undefined;
    } while (after);
  },
});
