import { numericInput } from "@/api/json";
import { localDay } from "@/features/fields/values";
import type { FilterExpr } from "@/api/types";

export interface FormSchema {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  properties?: Record<string, FormSchema>;
  additionalProperties?: boolean | FormSchema;
  required?: string[];
  items?: FormSchema;
  /** date (yyyy-MM-dd) and date-time (RFC 3339) are checked by the server; others are hints. */
  format?: string;
  /** "now" fills a date or date-time left out with the moment the form is sent. */
  "x-papergo-default"?: "now";
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
  disabled?: boolean;
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
/** Standalone has neither selection nor collection; item modes may span the workspace. */
export function manualItemMode(trigger: Trigger | undefined): 'none' | 'single' | 'multiple' {
  if (!trigger || trigger.type !== 'manual') return 'none';
  if (trigger.selection === 'selection') return 'multiple';
  return trigger.collection_id || trigger.selection === 'per_item' ? 'single' : 'none';
}
/** What a workflow version's script declares: when it runs and its forms. */
export interface WorkflowDefinition {
  triggers: Trigger[];
  condition?: FilterExpr;
  input_schema?: FormSchema;
  output_schema?: FormSchema;
  concurrency?: 'parallel' | 'skip' | 'replace' | 'coalesce';
}
export interface WorkflowCatalog {
  triggers: { type: string; description: string }[];
}

/** Whether a property is a date or date-time that defaults to the moment the form is sent. */
export function defaultsToNow(schema: FormSchema): boolean {
  return schema["x-papergo-default"] === "now" && (schema.format === "date" || schema.format === "date-time");
}

/** The moment the form is sent as a date or date-time property holds it, in the browser's time zone. */
export function nowValue(schema: FormSchema, now = new Date()): string {
  return schema.format === "date" ? localDay(now) : now.toISOString();
}

/**
 * Typed values as the server reads them: numbers as JSON numbers, a cleared date or date-time left out,
 * and a date or date-time that defaults to now filled with the moment it is sent, in the person's own
 * time zone rather than the server's.
 */
export function normalizeForm(schema: FormSchema, value: unknown, now = new Date()): unknown {
  if (value === undefined) return undefined;
  if (schema.type === "number" || schema.type === "integer")
    return typeof value === "string"
      ? numericInput(value, schema.type === "integer")
      : value;
  if (schema.type === "array")
    return (value as unknown[]).map((value) =>
      normalizeForm(schema.items ?? {}, value, now),
    );
  if (schema.type === "object" && schema.properties) {
    const out: Record<string, unknown> = {};
    for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
      const property = schema.properties[key] ?? {};
      if (entry === "" && (property.format === "date" || property.format === "date-time")) continue;
      out[key] = normalizeForm(property, entry, now);
    }
    for (const [key, property] of Object.entries(schema.properties))
      if (out[key] === undefined && defaultsToNow(property)) out[key] = nowValue(property, now);
    return out;
  }
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
