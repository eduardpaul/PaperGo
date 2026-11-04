import { compareNumber, isLosslessNumber } from 'lossless-json';
import { decimalText, numericInput, stringifyJSON } from '@/api/json';
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
  // Decimals travel as their exact text, written with a decimal point.
  if (typeof value === 'string' && type === 'decimal') return decimalText(value);
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

const pad = (part: number) => String(part).padStart(2, '0');

/** A moment as a datetime-local input shows it: wall-clock time in the browser's time zone. */
export function toLocalInput(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return `${localDay(date)}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** A datetime-local input's value as a UTC moment; empty stays empty. */
export function fromLocalInput(value: string): string {
  return value ? new Date(value).toISOString() : '';
}

/** The calendar day (yyyy-MM-dd) of a moment in the browser's time zone, as a date input holds it. */
export function localDay(date: Date): string {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}
