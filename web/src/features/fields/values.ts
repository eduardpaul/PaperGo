import { compareNumber, isLosslessNumber } from 'lossless-json';
import { numericInput, stringifyJSON } from '@/api/json';
import type { FieldDefinition } from '@/api/types';

export function sameValue(left: unknown, right: unknown): boolean {
  if ((typeof left === 'number' || isLosslessNumber(left)) && (typeof right === 'number' || isLosslessNumber(right))) {
    return compareNumber(String(left), String(right)) === 0;
  }
  return stringifyJSON(left ?? null) === stringifyJSON(right ?? null);
}

/** Merge patch of field values. Empty text and lists clear the value; numeric drafts retain exact digits. */
export function valuesPatch(
  fields: readonly (Pick<FieldDefinition, 'key'> & { type?: string })[],
  current: Record<string, unknown> | null | undefined,
  next: Record<string, unknown>,
): Record<string, unknown> | undefined {
  const patch: Record<string, unknown> = {};
  let changed = false;
  for (const field of fields) {
    const before = normalize(current?.[field.key]);
    const after = normalize(storedScalar(field.type, next[field.key]));
    if (!sameValue(before, after)) {
      patch[field.key] = after;
      changed = true;
    }
  }
  return changed ? patch : undefined;
}

function storedScalar(type: string | undefined, value: unknown): unknown {
  if (typeof value !== 'string' || (type !== 'number' && type !== 'currency' && type !== 'integer')) return value;
  if (value === '') return '';
  return numericInput(value, type === 'integer');
}

function normalize(value: unknown): unknown {
  if (value === undefined || value === '' || (Array.isArray(value) && value.length === 0)) return null;
  return value;
}

export function asList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String);
  if (value === undefined || value === null || value === '') return [];
  return [String(value)];
}

export function fieldText(value: unknown): string {
  if (value === undefined || value === null) return '';
  return String(value);
}
