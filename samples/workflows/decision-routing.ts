// Routes a request to a team with a local decision: started by hand with
// the request's subject and body, it returns the team.
import { json, plain, workflow } from "@papergo/workflow";

interface Request {
  subject: string;
  body: string;
  customer_tier: string;
}

export default workflow<Request, { route: string }>({
  triggers: [{ type: "manual" }],
  input_schema: {
    type: "object",
    required: ["subject", "body"],
    properties: { subject: { type: "string" }, body: { type: "string" }, customer_tier: { type: "string", default: "standard" } },
  },
  output_schema: { type: "object", properties: { route: { type: "string" } } },
  async run(ctx, input) {
    const decided = await ctx.wait((await ctx.api.v1.ai.decisions.post({
      workspaceId: ctx.run.workspace_id,
      data: json({ subject: input.subject, body: input.body, customer_tier: input.customer_tier }),
      questions: [{
        key: "route", type: "choice", instructions: "Which team should handle this request?",
        options: [
          { key: "billing", text: "Invoices, duplicate charges, payments and refunds" },
          { key: "technical", text: "Product failures, errors and technical troubleshooting" },
          { key: "other", text: "Requests outside billing and technical support" },
        ],
      }],
    }))!);
    const answers = (plain(decided.result) as { answers: Record<string, { selected: string }> }).answers;
    return { route: answers.route.selected };
  },
});
