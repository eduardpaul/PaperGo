// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { typingIn } from './keys';

it('treats fields, editable elements and Monaco editors as typing', () => {
  document.body.innerHTML = `
    <input id="field"><div contenteditable="true" id="editable"></div><div contenteditable="false" id="fixed"></div>
    <div class="monaco-editor"><div class="native-edit-context" id="monaco"></div></div><button id="button"></button>`;
  const at = (id: string) => document.getElementById(id);
  expect(typingIn(at('field'))).toBe(true);
  expect(typingIn(at('editable'))).toBe(true);
  expect(typingIn(at('monaco'))).toBe(true);
  expect(typingIn(at('fixed'))).toBe(false);
  expect(typingIn(at('button'))).toBe(false);
  expect(typingIn(null)).toBe(false);
});
