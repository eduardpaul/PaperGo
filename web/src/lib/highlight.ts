/** A run of text, marked when it matched the search. */
export interface TextPart {
  text: string;
  mark: boolean;
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/**
 * Cuts text into marked and plain runs. Ranges are UTF-8 byte offsets, as the
 * search API gives them; ranges that overlap, run past the end or split a
 * character are clamped or skipped rather than trusted.
 */
export function highlightParts(text: string, ranges: readonly (readonly [number, number])[] = []): TextPart[] {
  const bytes = encoder.encode(text);
  const sorted = [...ranges].filter(([start, end]) => start < end).sort((a, b) => a[0] - b[0]);
  const parts: TextPart[] = [];
  let at = 0;
  const push = (from: number, to: number, mark: boolean) => {
    if (to <= from) return;
    const piece = decoder.decode(bytes.subarray(from, to));
    if (piece.includes('�') && !text.includes('�')) return;
    parts.push({ text: piece, mark });
  };
  for (const [rawStart, rawEnd] of sorted) {
    const start = Math.max(rawStart, at);
    const end = Math.min(rawEnd, bytes.length);
    if (start >= end) continue;
    push(at, start, false);
    push(start, end, true);
    at = end;
  }
  push(at, bytes.length, false);
  return parts.length > 0 || text === '' ? parts : [{ text, mark: false }];
}
