// @papergo/workflow is the API a PaperGo workflow script is written against.
// A script's default export is a workflow():
//
//   import { workflow } from "@papergo/workflow";
//
//   export default workflow({
//     async run(ctx, input: { listId: string }) {
//       const item = await ctx.api.v1.resources.byId(input.listId).children.post({
//         kind: "item",
//         name: "Created by a workflow",
//       });
//       return { id: item?.id };
//     },
//   });
//
// The engine replays a run from the start each time it resumes it. Every API
// call, action, clock read and random value is a recorded step: on replay it
// returns the recorded result instead of happening again. Keep scripts
// deterministic: read time, randomness and data only through ctx and the
// globals the runtime replaces (Date, Math.random, crypto.randomUUID).

import { createPaperGoClient, type PaperGoClient } from "./client/paperGoClient.js";
import { HostRequestAdapter } from "./adapter.js";
import { host } from "./host.js";

export type { PaperGoClient } from "./client/paperGoClient.js";
export * as models from "./client/models/index.js";

export interface Context {
  /** The run being executed. */
  readonly runId: string;
  /** The PaperGo REST API, acting as the workflow version's author. Each call is one exactly-once step. */
  readonly api: PaperGoClient;
  /**
   * Runs a platform action, such as OCR, that completes outside the run.
   * The run is unloaded until the action completes, then replayed.
   */
  action<T = unknown>(name: string, input?: unknown): Promise<T>;
  /** Writes to the run's log. A replay does not repeat earlier entries. */
  log(...values: unknown[]): void;
  warn(...values: unknown[]): void;
  error(...values: unknown[]): void;
}

export interface Workflow<I = unknown, O = unknown> {
  run(ctx: Context, input: I): O | Promise<O>;
}

/** Declares a workflow. A script's default export must be one. */
export function workflow<I = unknown, O = unknown>(definition: Workflow<I, O>): Workflow<I, O> {
  return definition;
}

function format(values: unknown[]): string {
  return values
    .map((value) => {
      if (typeof value === "string") return value;
      if (value instanceof Error) return value.stack ?? `${value.name}: ${value.message}`;
      try {
        return JSON.stringify(value);
      } catch {
        return String(value);
      }
    })
    .join(" ");
}

const api = createPaperGoClient(new HostRequestAdapter());

(globalThis as Record<string, unknown>).crypto = { randomUUID: () => host.uuid() };

function context(runId: string): Context {
  return {
    runId,
    api,
    action<T>(name: string, input?: unknown): Promise<T> {
      return new Promise<T>((resolve) => {
        resolve(JSON.parse(host.action(name, JSON.stringify(input ?? null))) as T);
      });
    },
    log: (...values) => host.log("info", format(values)),
    warn: (...values) => host.log("warn", format(values)),
    error: (...values) => host.log("error", format(values)),
  };
}

/**
 * Runs definition for the engine; scripts do not call it. It resolves to the
 * run's output as JSON text.
 */
export async function __start(definition: unknown, runId: string, input: string): Promise<string> {
  if (!definition || typeof (definition as Workflow).run !== "function") {
    throw new TypeError("the script's default export must be workflow({ run })");
  }
  const output = await (definition as Workflow).run(context(runId), JSON.parse(input));
  return JSON.stringify(output ?? null);
}
