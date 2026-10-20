// @papergo/workflow is the API a PaperGo workflow script is written against.
// A script's default export is a workflow(): when it runs, and what it does.
//
//   import { workflow } from "@papergo/workflow";
//
//   export default workflow({
//     triggers: [{ type: "file.added", media_types: ["application/pdf"] }],
//     async run(ctx) {
//       const read = await ctx.api.v1.items.byId(ctx.itemId!).text.post({});
//       await ctx.wait(read!);
//       return { read: read!.id };
//     },
//   });
//
// Long work, such as OCR or an approval, is an API call that returns a
// pending operation. ctx.wait(operation) unloads the run until it finishes.
//
// The engine replays a run from the start each time it resumes it. Every API
// call, wait, sleep, clock read and random value is a recorded step: on
// replay it returns the recorded result instead of happening again. Keep
// scripts deterministic: read time, randomness and data only through ctx and
// the globals the runtime replaces (Date, Math.random, crypto.randomUUID).

import { isUntypedArray, isUntypedNode, isUntypedObject, type UntypedNode } from "@microsoft/kiota-abstractions";
import { createPaperGoClient, type PaperGoClient } from "./client/paperGoClient.js";
import type { Operation } from "./client/models/index.js";
import { HostRequestAdapter } from "./adapter.js";
import { host } from "./host.js";

export type { PaperGoClient } from "./client/paperGoClient.js";
export * as models from "./client/models/index.js";

/**
 * When a workflow runs. Item and folder events, schedules, manual starts,
 * task events, permission changes, term changes, and events of other
 * workflows (wf.{key}.completed) are trigger types; docs/workflows.md lists
 * them with their options.
 */
export interface Trigger {
  type: string;
  disabled?: boolean;
  collection_id?: string;
  content_type_id?: string;
  cron?: string;
  time_zone?: string;
  surface?: "head" | "published";
  selection?: "per_item" | "selection";
  terms?: string[];
  term_change?: "present" | "added" | "removed";
  sources?: string[];
  media_types?: string[];
  task_key?: string;
  actions?: string[];
}

/** What a start does while a run on the same item is active. */
export type Concurrency = "parallel" | "skip" | "replace" | "coalesce";

/** The event that started a run. */
export interface RunEvent {
  id: string;
  type: string;
  workspace_id: string;
  collection_id?: string;
  resource_id?: string;
  actor: string;
  depth: number;
  data: Record<string, unknown>;
}

/** Content captured once for the triggering revision; use the API for current state and ETags. */
export interface TriggerResource {
  readonly id: string;
  readonly workspace_id: string;
  readonly collection_id: string;
  readonly revision_id: string;
  readonly revision_number: number;
  readonly content_type_id: string;
  readonly name: string;
  readonly keywords: readonly string[];
  /** Plain JSON field values. Absent when they would exceed the snapshot's 64 KiB budget. */
  readonly values?: Readonly<Record<string, unknown>>;
  /** True when values were excluded to keep the snapshot small; API reads return current state. */
  readonly values_omitted: boolean;
}

/** Metadata of the file version that caused the event; file bytes stay behind the API. */
export interface TriggerFile {
  readonly id: string;
  readonly version: number;
  readonly filename: string;
  readonly content_type: string;
  readonly size: number;
}

/** One manually selected item, in launch order. */
export interface TriggerSelection {
  readonly id: string;
  /** Readable content at dispatch; absent if inaccessible or deleted. */
  readonly snapshot?: TriggerResource;
}

/** A manual launch, including test runs of a script with form values and selected items. */
export interface ManualTrigger<I = unknown> {
  readonly kind: "manual";
  /** Submitted launch form values; the same object as run(ctx, input)'s second argument. */
  readonly input: I;
  /** The primary selected item's readable snapshot, when available. */
  readonly item?: TriggerResource;
  /** Every selected item in launch order, including the primary item. Empty for standalone starts. */
  readonly items: readonly TriggerSelection[];
}

/** Narrow on kind to access manual inputs and selection, or the triggering item, folder or file. */
export type TriggerObject<I = unknown> =
  | ManualTrigger<I>
  | { readonly kind: "item"; readonly item: TriggerResource }
  | { readonly kind: "folder"; readonly folder: TriggerResource }
  | { readonly kind: "file"; readonly item: TriggerResource; readonly file: TriggerFile };

/** The run being executed. */
export interface Run<P = Record<string, unknown>, I = unknown> {
  id: string;
  workflow: { id: string; key: string; version: number };
  workspace_id: string;
  /** The run's item; a selection run's primary item. */
  item_id?: string;
  /** A selection run's items, in order. */
  items: string[];
  event: RunEvent;
  /** Stable trigger snapshot, when the author can read its content. */
  trigger?: TriggerObject<I>;
  /** Who caused the run. */
  actor: string;
  params: P;
}

export interface Context<P = Record<string, unknown>, I = unknown> {
  readonly runId: string;
  readonly run: Run<P, I>;
  /** The workflow's parameters, such as a built-in's settings. */
  readonly params: P;
  /** The run's item, when it has one. */
  readonly itemId?: string;
  /**
   * Triggering revision content, persisted for replay (up to 64 KiB).
   * File events include file metadata and their item. Manual/test starts
   * expose input, primary item and the complete ordered selection.
   * Other standalone, deleted, purged or inaccessible resources have no snapshot.
   * Coalescing workflows should use the API when they need current state.
   */
  readonly trigger?: TriggerObject<I>;
  /** The PaperGo REST API, acting as the workflow version's author. Each call is one exactly-once step. */
  readonly api: PaperGoClient;
  /**
   * Waits until operations have finished and returns them as they ended:
   * operations the API returned, or their IDs, such as a task's
   * operation_id. The run is unloaded while it waits. Waiting for several
   * at once lets them run in parallel. A failed or cancelled operation
   * throws an OperationError.
   */
  wait(operation: Operation | string): Promise<Operation>;
  wait(operations: (Operation | string)[]): Promise<Operation[]>;
  /** Unloads the run for ms milliseconds, or until a time. */
  sleep(duration: number | { until: Date | string }): Promise<void>;
  /** Writes to the run's log. A replay does not repeat earlier entries. */
  log(...values: unknown[]): void;
  warn(...values: unknown[]): void;
  error(...values: unknown[]): void;
}

export interface Workflow<I = unknown, O = unknown, P = Record<string, unknown>> {
  /** When the workflow runs; a function of the parameters for built-ins. */
  triggers?: Trigger[] | ((params: P) => Trigger[]);
  /** A filter items must meet, as in POST /v1/resources/{id}/query; a function of the parameters for built-ins. */
  condition?: Record<string, unknown> | ((params: P) => Record<string, unknown>);
  /** The launch form of manual starts; its values are the run's input. */
  input_schema?: Record<string, unknown>;
  /** The form of what run returns, checked when the run completes. */
  output_schema?: Record<string, unknown>;
  concurrency?: Concurrency;
  run(ctx: Context<P, I>, input: I): O | Promise<O>;
}

/** Declares a workflow. A script's default export must be one. */
export function workflow<I = unknown, O = unknown, P = Record<string, unknown>>(definition: Workflow<I, O, P>): Workflow<I, O, P> {
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

/**
 * Wraps a free-form JSON object for a request field the API leaves open,
 * such as an item's values or an output schema.
 */
export function json<T extends Record<string, unknown>>(value: T): { additionalData: T } {
  return { additionalData: value };
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

async function wait(operations: (Operation | string)[]): Promise<Operation[]> {
  const ids = operations.map((op) => {
    const id = typeof op === "string" ? op : op?.id;
    if (!id) throw new TypeError("ctx.wait needs operations returned by the API, or their IDs");
    return id;
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

async function sleep(duration: number | { until: Date | string }): Promise<void> {
  let until: number;
  if (typeof duration === "number") {
    if (!(duration >= 0)) throw new TypeError("ctx.sleep takes milliseconds or { until }");
    until = Date.now() + duration;
  } else {
    until = new Date(duration.until).getTime();
    if (Number.isNaN(until)) throw new TypeError("ctx.sleep until must be a date");
  }
  host.sleep(until);
}

function context<P, I>(run: Run<P, I>): Context<P, I> {
  return {
    runId: run.id,
    run,
    params: run.params,
    itemId: run.item_id,
    trigger: run.trigger,
    api,
    wait: ((operations: Operation | string | (Operation | string)[]) =>
      Array.isArray(operations) ? wait(operations) : wait([operations]).then((all) => all[0])) as Context<P, I>["wait"],
    sleep,
    log: (...values) => host.log("info", format(values)),
    warn: (...values) => host.log("warn", format(values)),
    error: (...values) => host.log("error", format(values)),
  };
}

function check(definition: unknown): Workflow {
  if (!definition || typeof (definition as Workflow).run !== "function") {
    throw new TypeError("the script's default export must be workflow({ run })");
  }
  return definition as Workflow;
}

/**
 * Runs definition for the engine; scripts do not call it. run is the run's
 * JSON with its input; the promise resolves to the output as JSON text.
 */
export async function __start(definition: unknown, run: string): Promise<string> {
  const { input: providedInput, ...info } = JSON.parse(run) as Run & { input: unknown };
  const input = info.trigger?.kind === "manual" ? providedInput ?? {} : providedInput;
  if (info.trigger?.kind === "manual") {
    // Input and the primary snapshot are already persisted once. Expose
    // references here instead of duplicating them in the run payload.
    const trigger = info.trigger;
    Object.assign(trigger, {
      input,
      items: trigger.items ?? [],
      item: trigger.items?.find((selected) => selected.id === info.item_id)?.snapshot,
    });
  }
  const output = await check(definition).run(context(info), input);
  return JSON.stringify(output ?? null);
}

/**
 * Returns, as JSON text, what the engine reads when a version is saved:
 * the triggers for params and the other settings. run is not called.
 */
export function __manifest(definition: unknown, params: string): string {
  const w = check(definition);
  const p = JSON.parse(params);
  const triggers = typeof w.triggers === "function" ? w.triggers(p) : w.triggers;
  const condition = typeof w.condition === "function" ? w.condition(p) : w.condition;
  return JSON.stringify({
    triggers: triggers ?? [],
    condition,
    input_schema: w.input_schema,
    output_schema: w.output_schema,
    concurrency: w.concurrency,
  });
}
