/**
 * Whether a key goes to something the person types in: a form field, an
 * editable element, or a Monaco editor, whose input is a plain element when
 * the browser supports EditContext. Page shortcuts leave such keys alone.
 */
export function typingIn(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), .monaco-editor');
}
