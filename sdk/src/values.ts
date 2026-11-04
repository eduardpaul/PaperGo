// JSON values the generated client leaves open, for workflows and apps.

import { isUntypedArray, isUntypedNode, isUntypedObject, type UntypedNode } from "@microsoft/kiota-abstractions";

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
