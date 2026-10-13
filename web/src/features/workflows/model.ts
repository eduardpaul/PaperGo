import { isLosslessNumber } from 'lossless-json';
import { numericInput } from '@/api/json';
import type { FilterExpr } from '@/api/types';

export interface FormSchema {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  properties?: Record<string, FormSchema>;
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
  'x-papergo'?: {
    kind: string;
    collection_id?: string;
    term_set_id?: string;
    group_id?: string;
    term_ids?: string[];
    access?: string;
    relationship_type_id?: string;
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
}
export interface Activity {
  key: string;
  kind: string;
  description: string;
  ports: string[];
  terminal?: boolean;
  input_schema: FormSchema;
}
export interface WorkflowCatalog {
  triggers: { type: string; description: string }[];
  activities: Activity[];
}

export function normalizeForm(schema: FormSchema, value: unknown, tokens = false): unknown {
  if (value === undefined) return undefined;
  if (tokens && typeof value === 'string' && value.includes('{')) return value;
  if (schema.type === 'number' || schema.type === 'integer')
    return typeof value === 'string' ? numericInput(value, schema.type === 'integer') : value;
  if (schema.type === 'array')
    return (value as unknown[]).map((value) => normalizeForm(schema.items ?? {}, value, tokens));
  if (schema.type === 'object' && schema.properties)
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
          ? property.type === 'boolean'
            ? [[key, false]]
            : property.type === 'object'
              ? [[key, formDefaults(property)]]
              : property.type === 'array'
                ? [[key, []]]
                : []
          : [],
    ),
  );
}

/** Rename graph edges and output tokens together so the saved flow keeps its meaning. */
export function renameNode(definition: WorkflowDefinition, oldName: string, newName: string): WorkflowDefinition {
  newName = newName.trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9 _-]{0,99}$/.test(newName))
    throw new Error('Step names use letters, digits, spaces, underscores or hyphens.');
  if (newName !== oldName && definition.flow.nodes[newName]) throw new Error('A step already has this name.');
  const tokens = (value: unknown): unknown => {
    if (typeof value === 'string')
      return value
        .replaceAll(`{step:${oldName}.`, `{step:${newName}.`)
        .replaceAll(`{step:${oldName}}`, `{step:${newName}}`);
    if (isLosslessNumber(value)) return value;
    if (Array.isArray(value)) return value.map(tokens);
    if (value && typeof value === 'object')
      return Object.fromEntries(Object.entries(value).map(([key, value]) => [key, tokens(value)]));
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
            Object.entries(node.next ?? {}).map(([port, target]) => [port, target === oldName ? newName : target]),
          ),
        },
      ]),
    ),
  };
  return updated;
}
