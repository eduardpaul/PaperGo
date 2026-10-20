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
// Long work, such as OCR or an approval, is an API call that returns a
// pending operation. ctx.wait(operation) unloads the run until it finishes:
//
//   const bulk = await ctx.api.v1.resources.byId(listId).bulkUpdate.post({ values: { status: "done" } });
//   const finished = await ctx.wait(bulk!);
//
// The engine replays a run from the start each time it resumes it. Every API
// call, wait, clock read and random value is a recorded step: on replay it
// returns the recorded result instead of happening again. Keep scripts
// deterministic: read time, randomness and data only through ctx and the
// globals the runtime replaces (Date, Math.random, crypto.randomUUID).

import { isUntypedArray, isUntypedNode, isUntypedObject, type UntypedNode } from "@microsoft/kiota-abstractions";
import { createPaperGoClient, type PaperGoClient } from "./client/paperGoClient.js";
import type { Operation } from "./client/models/index.js";
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
   * Waits until operations the run started have finished and returns them
   * as they ended. The run is unloaded while it waits. Waiting for several
   * at once lets them run in parallel. A failed or superseded operation
   * throws an OperationError.
   */
  wait(operation: Operation): Promise<Operation>;
  wait(operations: Operation[]): Promise<Operation[]>;
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

/** An operation that ended without succeeding. */
export class OperationError extends Error {
  constructor(readonly operation: Operation, readonly operations: Operation[]) {
    super(`operation ${operation.id} ${operation.status}${operation.errorEscaped ? `: ${operation.errorEscaped}` : ""}`);
    this.name = "OperationError";
  }
}

/** Converts Kiota untyped values, such as an operation's result, to plain JSON values. */
export function plain(value: UntypedNode | null | undefined): unknown {
  if (value === null || value === undefined) return null;
  if (!isUntypedNode(value)) return value;
  if (isUntypedArray(value)) return value.getValue().map((item) => plain(item));
  if (isUntypedObject(value)) {
    const out: Record<string, unknown> = {};
    const fields = value.getValue();
    for (const key of Object.keys(fields)) out[key] = plain(fields[key]);
    return out;
  }
  return value.getValue() ?? null;
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
// console writes to the run's log, like ctx.log.
(globalThis as Record<string, unknown>).console = {
  log: (...values: unknown[]) => host.log("info", format(values)),
  info: (...values: unknown[]) => host.log("info", format(values)),
  debug: (...values: unknown[]) => host.log("info", format(values)),
  warn: (...values: unknown[]) => host.log("warn", format(values)),
  error: (...values: unknown[]) => host.log("error", format(values)),
};

async function wait(operations: Operation[]): Promise<Operation[]> {
  const ids = operations.map((op) => {
    if (!op?.id) throw new TypeError("ctx.wait needs operations returned by the API");
    return op.id;
  });
  host.wait(ids);
  const finished: Operation[] = [];
  for (const id of ids) {
    const op = await api.v1.operations.byId(id).get();
    if (!op) throw new Error(`operation ${id} is gone`);
    finished.push(op);
  }
  const failed = finished.find((op) => op.status !== "succeeded");
  if (failed) throw new OperationError(failed, finished);
  return finished;
}

function context(runId: string): Context {
  return {
    runId,
    api,
    wait: ((operations: Operation | Operation[]) =>
      Array.isArray(operations) ? wait(operations) : wait([operations]).then((all) => all[0])) as Context["wait"],
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
