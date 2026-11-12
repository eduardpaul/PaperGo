import glyphs from '@appicon/glyphs.json';
import { createElement } from 'react';

const outlines = glyphs as unknown as Record<string, [string, Record<string, string>][]>;

/** The glyphs an app may choose for its icon, which the server draws too (internal/appicon). */
export const glyphNames = Object.keys(outlines);

/** One of the glyphs, drawn in the text color. */
export function Glyph({ name, className }: { name: string; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden className={className}>
      {outlines[name]?.map(([tag, attributes], index) => createElement(tag, { key: index, ...attributes }))}
    </svg>
  );
}
