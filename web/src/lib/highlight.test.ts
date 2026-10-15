import { expect, it } from 'vitest';
import { highlightParts } from './highlight';

it('marks byte ranges, also after multi-byte characters', () => {
  expect(highlightParts('Café invoice', [[6, 13]])).toEqual([
    { text: 'Café ', mark: false },
    { text: 'invoice', mark: true },
  ]);
  expect(
    highlightParts('a b a', [
      [4, 5],
      [0, 1],
    ]),
  ).toEqual([
    { text: 'a', mark: true },
    { text: ' b ', mark: false },
    { text: 'a', mark: true },
  ]);
});

it('clamps overlapping and out-of-range highlights', () => {
  expect(
    highlightParts('abcdef', [
      [0, 4],
      [2, 5],
      [5, 99],
    ]),
  ).toEqual([
    { text: 'abcd', mark: true },
    { text: 'e', mark: true },
    { text: 'f', mark: true },
  ]);
  expect(highlightParts('plain')).toEqual([{ text: 'plain', mark: false }]);
  expect(highlightParts('')).toEqual([]);
});
