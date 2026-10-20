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

export function normalizeForm(schema: FormSchema, value: unknown): unknown {
  if (value === undefined) return undefined;
  if (schema.type === "number" || schema.type === "integer")
    return typeof value === "string"
      ? numericInput(value, schema.type === "integer")
      : value;
  if (schema.type === "array")
    return (value as unknown[]).map((value) =>
      normalizeForm(schema.items ?? {}, value),
    );
  if (schema.type === "object" && schema.properties)
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>).map(([key, value]) => [
        key,
        normalizeForm(schema.properties?.[key] ?? {}, value),
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
