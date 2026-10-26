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
// A long loop continues as new to keep its journal short: the run starts its
// script again, with what it carries as ctx.state and a fresh journal.
//
//   const { cursor } = (ctx.state ?? {}) as { cursor?: string };
//   ... process one page ...
//   if (next) ctx.continueAsNew({ cursor: next });
//
// Long work, such as OCR or an approval, is an API call that returns a
// pending operation. ctx.wait(operation) unloads the run until it finishes.
// Waits a script starts together, such as those of tasks it asks in a
// Promise.all, are one wait: the run unloads once, until all have finished.
//
//   const [legal, finance] = await Promise.all([
//     ctx.ask({ title: "Legal review", assignees: ["group:legal"] }),
//     ctx.ask({ title: "Budget check", assignees: ["group:finance"] }),
//   ]);
//
// Work on many items fans out to runs of a workflow with a call trigger,
// each with its own small journal, running in parallel:
//
//   const totals = await Promise.all(items.map((id) => ctx.call("check-invoice", {}, { itemId: id })));
//
// A list that comes in pages, such as a folder's children, also has all(),
// which reads every page and returns their values:
//
//   const children = await ctx.api.v1.resources.byId(folderId).children.all();
//
// The engine replays a run from the start each time it resumes it. Every API
// call, wait, sleep, clock read and random value is a recorded step: on
// replay it returns the recorded result instead of happening again. Keep
// scripts deterministic: read time, randomness and data only through ctx and
// the globals the runtime replaces (Date, Math.random, crypto.randomUUID).

import { isUntypedArray, isUntypedNode, isUntypedObject, type UntypedNode } from "@microsoft/kiota-abstractions";
import { createPaperGoClient, PaperGoClientNavigationMetadata, type PaperGoClient } from "./client/paperGoClient.js";
import { createOperationFromDiscriminatorValue, type NotificationSend, type NotificationSendResult, type Operation, type TaskCreate } from "./client/models/index.js";
import { HostRequestAdapter, parseCollection } from "./adapter.js";
import { host } from "./host.js";
import { client } from "./builders.js";

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
  /** A content type (a catalog type, or a list type standing for it); matches its subtypes too. */
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
  /** Key of the catalog type the item's type copies, such as `invoice`. */
  readonly content_type_key: string;
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

/** A run another run started with ctx.call. The called run reads its item through the API. */
export interface CallTrigger<I = unknown> {
  readonly kind: "call";
  /** The call's input; the same object as run(ctx, input)'s second argument. */
  readonly input: I;
  /** The run that called this one. */
  readonly parent_run_id: string;
}

/** Narrow on kind to access manual inputs and selection, a call, or the triggering item, folder or file. */
export type TriggerObject<I = unknown> =
  | ManualTrigger<I>
  | CallTrigger<I>
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
  /** How many times the run continued as new. */
  generation: number;
  /** What the run's last generation carried into this one; absent in the first. */
  state?: unknown;
}

/** A task ctx.ask creates: the API's task fields, with its form and values as plain JSON. */
export type TaskRequest<V = Record<string, unknown>> = Omit<TaskCreate, "title" | "inputSchema" | "values"> & {
  title: string;
  /** The answer's form, a JSON Schema form; see docs/workflows.md#forms. */
  inputSchema?: Record<string, unknown>;
  /** Values that prefill the form. */
  values?: Partial<V>;
  /** The workspace of the task; the run's by default. */
  workspaceId?: string;
};

/** One person's answer to a task. */
export interface TaskResponse {
  readonly subject: string;
  /** The assignee entry answered for: the subject or group:<id>. */
  readonly assignee: string;
  readonly outcome: string;
  readonly comment: string;
  readonly input?: Record<string, unknown>;
}

/** How a task ended. V is the shape of its form's values. */
export interface TaskResult<V = Record<string, unknown>> {
  readonly task_id: string;
  readonly status: "completed" | "expired";
  /** The key of the outcome the task completed with. */
  readonly outcome: string;
  readonly comment: string;
  readonly completed_by?: string;
  /** The form values of the completing answer. */
  readonly input?: V;
  /** Every answer, oldest first. */
  readonly responses: readonly TaskResponse[];
}

/** A message ctx.notify sends: the API's fields, in the run's workspace by default. */
export type NotificationRequest = Omit<NotificationSend, "recipients" | "title"> & {
  /** People, group:<id>, role:owners, role:members, creator, actor or field:<key>. */
  recipients: string[];
  title: string;
  workspaceId?: string;
};

/** Where a called run works. */
export interface CallOptions {
  /** The item the called run works on; none for a workflow called without one. */
  itemId?: string;
}

export interface Context<P = Record<string, unknown>, I = unknown> {
  readonly runId: string;
  readonly run: Run<P, I>;
  /** The workflow's parameters, such as a built-in's settings. */
  readonly params: P;
  /** The run's item, when it has one. */
  readonly itemId?: string;
  /** What the last generation passed to continueAsNew; undefined in the first. Cast it to its shape. */
  readonly state?: unknown;
  /** How many times the run continued as new; 0 in the first generation. */
  readonly generation: number;
  /**
   * Triggering revision content, persisted for replay (up to 64 KiB).
   * File events include file metadata and their item. Manual/test starts
   * expose input, primary item and the complete ordered selection.
   * Other standalone, deleted, purged or inaccessible resources have no snapshot.
   * Coalescing workflows should use the API when they need current state.
   */
  readonly trigger?: TriggerObject<I>;
  /**
   * The PaperGo REST API, acting as the workflow version's author. Each call is one exactly-once step.
   * Paged lists have all(), which reads every page, one step each, and returns their values.
   */
  readonly api: PaperGoClient;
  /**
   * Waits until operations have finished and returns them as they ended:
   * operations the API returned, or their IDs, such as a task's
   * operation_id. The run is unloaded while it waits. Waits started
   * together, such as in a Promise.all, are one: the run unloads once,
   * until all have finished. A failed or cancelled operation throws an
   * OperationError.
   */
  wait(operation: Operation | string): Promise<Operation>;
  wait(operations: (Operation | string)[]): Promise<Operation[]>;
  /**
   * Creates a task and returns its result once people have answered it. The
   * task is created at once; the run waits only when it awaits the result,
   * so tasks asked together wait together:
   *
   *     const [a, b] = await Promise.all([ctx.ask(first), ctx.ask(second)]);
   *
   * A cancelled task throws an OperationError.
   */
  ask<V = Record<string, unknown>>(task: TaskRequest<V>): Promise<TaskResult<V>>;
  /** Sends a message to people's inboxes; it needs no answer. Sending again with the same dedupKey reaches only people who do not have it. */
  notify(message: NotificationRequest): Promise<NotificationSendResult>;
  /**
   * Runs the workflow with key workflow, which must declare a call trigger,
   * with input and, in options, the item it works on, and returns what it
   * returned. The run starts at once and belongs to this one: cancelling
   * this run cancels it. It acts as its own version's author. Calls made
   * together start in batches and wait together, so a fan-out over many
   * items costs this run a few steps:
   *
   *     await Promise.all(ids.map((id) => ctx.call("check-invoice", {}, { itemId: id })));
   *
   * A called run that fails throws an OperationError.
   */
  call<O = unknown, CI = Record<string, unknown>>(workflow: string, input?: CI, options?: CallOptions): Promise<O>;
  /** Unloads the run for ms milliseconds, or until a time. */
  sleep(duration: number | { until: Date | string }): Promise<void>;
  /**
   * Ends this generation of the run and starts its script again with a
   * fresh journal, reading state, at most 64 KiB of JSON, as ctx.state. The
   * run keeps its ID, input, item, logs and what it started, and whoever
   * waits for it waits for its last generation. Every replay repeats the
   * whole journal, so a loop over many pages or items that continues every
   * few hundred steps stays fast. Await the calls and waits it started
   * first. It does not return.
   */
  continueAsNew(state?: unknown): never;
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

const adapter = new HostRequestAdapter();
// Kiota's client registers the serializers; scripts use the SDK's builders.
createPaperGoClient(adapter);
const api = client<PaperGoClient>(adapter, PaperGoClientNavigationMetadata);

(globalThis as Record<string, unknown>).crypto = { randomUUID: () => host.uuid() };
// console writes to the run's log, like ctx.log.
(globalThis as Record<string, unknown>).console = {
  log: (...values: unknown[]) => host.log("info", format(values)),
  info: (...values: unknown[]) => host.log("info", format(values)),
  debug: (...values: unknown[]) => host.log("info", format(values)),
  warn: (...values: unknown[]) => host.log("warn", format(values)),
  error: (...values: unknown[]) => host.log("error", format(values)),
};

// A wait the script started that the engine has not yet waited for.
interface Waiting {
  ids: string[];
  resolve(operations: Operation[]): void;
  reject(error: unknown): void;
}

let waiting: Waiting[] = [];

// The most operations one wait step takes (maxWaits in internal/script).
const waitLimit = 64;

function wait(operations: (Operation | string)[]): Promise<Operation[]> {
  return new Promise((resolve, reject) => {
    const ids = operations.map((op) => {
      const id = typeof op === "string" ? op : op?.id;
      if (!id) throw new TypeError("ctx.wait needs operations returned by the API, or their IDs");
      return id;
    });
    waiting.push({ ids, resolve, reject });
  });
}

// A call the script made that has not been sent yet.
interface Calling {
  workspace: string;
  workflow: string;
  itemId?: string;
  input?: Record<string, unknown>;
  resolve(runId: string): void;
  reject(error: unknown): void;
}

let calling: Calling[] = [];

// The most calls one request makes (workflow.MaxCalls).
const callLimit = 500;

// The engine calls __papergoIdle when the script has nothing left to run
// but its run is not over. It first sends the calls made since, a request
// per workflow and 500 calls, then their waits start with the others: every
// wait started since is one wait step, so the run unloads once, until all
// its operations have finished. It returns whether it sent or waited.
function idle(): boolean {
  if (calling.length > 0) {
    const made = calling;
    calling = [];
    void send(made);
    return true;
  }
  if (waiting.length === 0) return false;
  const started = waiting;
  waiting = [];
  finish(started);
  return true;
}

// finish waits for the operations of started, which the wait step returns
// as they ended, and settles each wait.
function finish(started: Waiting[]): void {
  const ids = [...new Set(started.flatMap((w) => w.ids))];
  const ended = new Map<string, Operation>();
  try {
    for (let i = 0; i < ids.length; i += waitLimit) {
      for (const op of parseCollection<Operation>(host.wait(ids.slice(i, i + waitLimit)), createOperationFromDiscriminatorValue)) ended.set(op.id!, op);
    }
  } catch (error) {
    for (const w of started) w.reject(error);
    return;
  }
  for (const w of started) {
    const gone = w.ids.find((id) => !ended.has(id));
    if (gone) {
      w.reject(new Error(`operation ${gone} is gone`));
      continue;
    }
    const operations = w.ids.map((id) => ended.get(id)!);
    const failed = operations.find((op) => op.status !== "succeeded");
    if (failed) w.reject(new OperationError(failed, operations));
    else w.resolve(operations);
  }
}

(globalThis as Record<string, unknown>).__papergoIdle = idle;

async function send(made: Calling[]): Promise<void> {
  const byWorkflow = new Map<string, Calling[]>();
  for (const c of made) {
    const key = `${c.workspace}/${c.workflow}`;
    byWorkflow.set(key, [...(byWorkflow.get(key) ?? []), c]);
  }
  for (const calls of byWorkflow.values()) {
    for (let i = 0; i < calls.length; i += callLimit) {
      const batch = calls.slice(i, i + callLimit);
      try {
        const started = await api.v1.workspaces.byId(batch[0].workspace).workflowCalls.post({
          workflow: batch[0].workflow,
          calls: batch.map((c) => ({ itemId: c.itemId, input: c.input && json(c.input) })),
        });
        batch.forEach((c, n) => c.resolve(started!.runIds![n]));
      } catch (error) {
        for (const c of batch) c.reject(error);
      }
    }
  }
}

async function call<O>(workspace: string, workflow: string, input: Record<string, unknown> | undefined, options: CallOptions | undefined): Promise<O> {
  const runId = await new Promise<string>((resolve, reject) => calling.push({ workspace, workflow, itemId: options?.itemId, input, resolve, reject }));
  const [operation] = await wait([runId]);
  return plain(operation.result) as O;
}

async function ask<V>(workspaceId: string, request: TaskRequest<V>): Promise<TaskResult<V>> {
  const { workspaceId: workspace, inputSchema, values, ...task } = request;
  const body: TaskCreate = { ...task };
  if (inputSchema) body.inputSchema = json(inputSchema);
  if (values) body.values = json(values as Record<string, unknown>);
  const created = await api.v1.workspaces.byId(workspace ?? workspaceId).tasks.post(body);
  const [operation] = await wait([created!.operationId!]);
  return plain(operation.result) as TaskResult<V>;
}

async function notify(workspaceId: string, message: NotificationRequest): Promise<NotificationSendResult> {
  const { workspaceId: workspace, ...send } = message;
  return (await api.v1.workspaces.byId(workspace ?? workspaceId).notifications.post(send))!;
}

function continueAsNew(state: unknown): never {
  if (calling.length > 0 || waiting.length > 0) {
    throw new TypeError("await ctx.call and ctx.wait before ctx.continueAsNew: the next generation would not see what they return");
  }
  host.continue(JSON.stringify(state ?? null));
  // The engine stops the script; nothing runs after host.continue.
  throw new Error("the run continued as new");
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
    state: run.state ?? undefined,
    generation: run.generation ?? 0,
    continueAsNew,
    api,
    wait: ((operations: Operation | string | (Operation | string)[]) =>
      Array.isArray(operations) ? wait(operations) : wait([operations]).then((all) => all[0])) as Context<P, I>["wait"],
    ask: (task) => ask(run.workspace_id, task),
    notify: (message) => notify(run.workspace_id, message),
    call: (<O>(workflow: string, input?: Record<string, unknown>, options?: CallOptions) => call<O>(run.workspace_id, workflow, input, options)) as Context<P, I>["call"],
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
  const kind = info.trigger?.kind;
  const input = kind === "manual" || kind === "call" ? providedInput ?? {} : providedInput;
  if (info.trigger?.kind === "call") Object.assign(info.trigger, { input });
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
