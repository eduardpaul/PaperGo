import { isLosslessNumber } from "lossless-json";
import { numericInput } from "@/api/json";
import type { FilterExpr } from "@/api/types";

export interface FormSchema {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  properties?: Record<string, FormSchema>;
  additionalProperties?: boolean | FormSchema;
  "x-term-set-sibling"?: string;
  "x-term-set-bound"?: boolean;
  "x-decision-questions"?: boolean;
  required?: string[];
  items?: FormSchema;
  format?: string;
  readOnly?: boolean;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  "x-papergo"?: {
    kind: string;
    select?: "terms" | "sets";
    collection_id?: string;
    term_set_id?: string;
    group_id?: string;
    term_ids?: string[];
    access?: string;
    relationship_type_id?: string;
  };
  /** Launch screen hints of a selection run. */
  "x-papergo-selection"?: {
    preview?: string;
    item_label?: string;
    order_label?: string;
    primary_description?: string;
  };
}
export interface Trigger {
  type: string;
  collection_id?: string;
  content_type_id?: string;
  cron?: string;
  time_zone?: string;
  surface?: string;
  selection?: string;
  terms?: string[];
  term_change?: string;
  sources?: string[];
  /** On file.added and file.updated: media types (application/pdf) or wildcards (image/*). */
  media_types?: string[];
  task_key?: string;
  actions?: string[];
}
export interface FlowNode {
  activity: string;
  inputs?: Record<string, unknown>;
  next?: Record<string, string>;
  retry?: { attempts: number; delay?: string };
}
export interface WorkflowDefinition {
  triggers: Trigger[];
  condition?: FilterExpr;
  input_schema?: FormSchema;
  variables?: Record<string, unknown>;
  concurrency?: string;
  flow: { start: string; nodes: Record<string, FlowNode> };
  /** Form of what a completed run returns; outputs maps its properties to values or tokens. */
  output_schema?: FormSchema;
  outputs?: Record<string, unknown>;
}
export interface Activity {
  key: string;
  kind: string;
  description: string;
  ports: string[];
  /** A node's ports come from this input instead: a task node's outcomes. */
  ports_from?: "outcomes";
  terminal?: boolean;
  input_schema: FormSchema;
  output_schema?: FormSchema;
}

/** The ports a node can connect besides done and error, as the server checks them. */
export function nodePorts(
  activity: Activity | undefined,
  inputs: Record<string, unknown> | undefined,
): string[] {
  if (!activity) return [];
  if (activity.ports_from !== "outcomes") return activity.ports;
  if (inputs?.wait === false) return [];
  const outcomes = Array.isArray(inputs?.outcomes)
    ? (inputs.outcomes as { key?: unknown }[])
    : [{ key: "done" }];
  return outcomes
    .map((o) => (typeof o?.key === "string" ? o.key : ""))
    .filter(Boolean);
}
export interface WorkflowCatalog {
  triggers: { type: string; description: string }[];
  activities: Activity[];
}

export function normalizeForm(
  schema: FormSchema,
  value: unknown,
  tokens = false,
): unknown {
  if (value === undefined) return undefined;
  if (tokens && typeof value === "string" && value.includes("{")) return value;
  if (schema.type === "number" || schema.type === "integer")
    return typeof value === "string"
      ? numericInput(value, schema.type === "integer")
      : value;
  if (schema.type === "array")
    return (value as unknown[]).map((value) =>
      normalizeForm(schema.items ?? {}, value, tokens),
    );
  if (schema.type === "object" && schema.properties)
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>).map(([key, value]) => [
        key,
        normalizeForm(schema.properties?.[key] ?? {}, value, tokens),
      ]),
    );
  return value;
}

export function formDefaults(schema: FormSchema): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(schema.properties ?? {}).flatMap(([key, property]) =>
      property.default !== undefined
        ? [[key, property.default]]
        : schema.required?.includes(key)
          ? property.type === "boolean"
            ? [[key, false]]
            : property.type === "object"
              ? [[key, formDefaults(property)]]
              : property.type === "array"
                ? [[key, []]]
                : []
          : [],
    ),
  );
}

/** Rename graph edges and output tokens together so the saved flow keeps its meaning. */
export function renameNode(
  definition: WorkflowDefinition,
  oldName: string,
  newName: string,
): WorkflowDefinition {
  newName = newName.trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9 _-]{0,99}$/.test(newName))
    throw new Error(
      "Step names use letters, digits, spaces, underscores or hyphens.",
    );
  if (newName !== oldName && definition.flow.nodes[newName])
    throw new Error("A step already has this name.");
  const tokens = (value: unknown): unknown => {
    if (typeof value === "string")
      return value
        .replaceAll(`{step:${oldName}.`, `{step:${newName}.`)
        .replaceAll(`{step:${oldName}}`, `{step:${newName}}`);
    if (isLosslessNumber(value)) return value;
    if (Array.isArray(value)) return value.map(tokens);
    if (value && typeof value === "object")
      return Object.fromEntries(
        Object.entries(value).map(([key, value]) => [key, tokens(value)]),
      );
    return value;
  };
  const updated = tokens(definition) as WorkflowDefinition;
  updated.flow = {
    start: definition.flow.start === oldName ? newName : definition.flow.start,
    nodes: Object.fromEntries(
      Object.entries(updated.flow.nodes).map(([name, node]) => [
        name === oldName ? newName : name,
        {
          ...node,
          next: Object.fromEntries(
            Object.entries(node.next ?? {}).map(([port, target]) => [
              port,
              target === oldName ? newName : target,
            ]),
          ),
        },
      ]),
    ),
  };
  return updated;
}

/** Token paths use the authored question keys and the catalog's output schema. */
export function activityOutputPaths(activity: Activity | undefined, inputs: Record<string, unknown> | undefined): string[] {
 if (activity?.key === 'ai.decide' && Array.isArray(inputs?.questions)) {
  return inputs.questions.flatMap((q: {key?: string; type?: string} | null) => q?.key ?
   ['selected','probability','probabilities',...(q.type==='noul'?['noul']:q.type==='score'?['score']:[])].map(field=>`answers.${q.key}.${field}`) : []);
 }
 return Object.keys(activity?.output_schema?.properties ?? {});
}

export function decisionQuestionErrors(questions: unknown[]): string[] {
 const errors:string[]=[];const keys=new Set<string>();
 if (questions.length<1||questions.length>32) errors.push('Use 1–32 questions.');
 let total=0;
 for (const [index,raw] of questions.entries()) {
  if (!raw||typeof raw!=='object') continue;
  const q=raw as {key?:string;type?:string;instructions?:string;options?:{key?:string;text?:string}[]|string};
  const label=`Question ${index+1}`;
  if (!q.key?.trim()) errors.push(`${label}: a key is required.`);
  if (q.key&&!q.key.includes('{')) {
   if (!/^[a-z][a-z0-9_]{0,63}$/.test(q.key)||keys.has(q.key)) errors.push(`${label}: use a unique lowercase identifier.`);
   keys.add(q.key);
  }
  if (!q.instructions?.trim()) errors.push(`${label}: instructions are required.`);
  // Token mode holds a string, including an empty one while it is being edited.
  // Its array shape and option semantics are checked after runtime resolution.
  if (typeof q.options==='string') continue;
  const options=Array.isArray(q.options)?q.options:[];total+=options.length;
  if (options.length<2||options.length>20) errors.push(`${label}: use 2–20 ordered options.`);
  if (q.type==='noul' && (options.length!==2||options[0]?.key!=='false'||options[1]?.key!=='true')) errors.push(`${label}: noul requires false then true, in that order.`);
  const optionKeys=new Set<string>(),texts=new Set<string>();
  for (const [i,option] of options.entries()) {
   if (!option.key?.trim()) errors.push(`${label}: option keys are required.`);
   if (option.key&&!option.key.includes('{') && (!/^[A-Za-z0-9_-]{1,64}$/.test(option.key)||optionKeys.has(option.key))) errors.push(`${label}: option keys must be unique, without dots.`);
   if (option.key) optionKeys.add(option.key);
   const text=q.type==='score'?`level ${i}: ${option.text}`:q.type==='noul'?`${option.key}: ${option.text}`:option.text;
   if (!option.text?.trim() || (text && texts.has(text))) errors.push(`${label}: use nonempty, distinct rendered option texts.`);
   if (text) texts.add(text);
  }
 }
 if (total>256) errors.push('Use at most 256 option entries across all questions.');
 return [...new Set(errors)];
}
