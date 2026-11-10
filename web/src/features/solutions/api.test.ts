// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { setToken } from '@/api/http';
import { noChoices, optionsOf, sendSolution } from './api';

afterEach(() => {
  vi.restoreAllMocks();
});

const result = { solution: 'tank', name: 'Tank', version: '1.1.0', applied: false, changes: [], customized: [], warnings: [], slots: [], settings: {}, paused: false, languages: [] };

it('sends the packed solution with the choices, and returns a refused apply as its result', async () => {
  setToken('token');
  const refused = { ...result, refusals: [{ kind: 'workflow', key: 'tank-levels', reason: 'was edited', conflict: true }] };
  const fetch = vi
    .spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(new Response(JSON.stringify(refused), { status: 409, headers: { 'Content-Type': 'application/json' } }));
  const file = new Blob(['zip']);
  await expect(sendSolution('w1', file, 'apply', { settings: { capacity_l: 900 }, paused: true })).resolves.toEqual(refused);
  const [url, init] = fetch.mock.calls[0];
  const sent = new URL(String(url), 'http://localhost');
  expect(sent.pathname).toBe('/v1/workspaces/w1/solutions/apply');
  expect(JSON.parse(sent.searchParams.get('options') ?? '')).toEqual({ settings: { capacity_l: 900 }, paused: true });
  expect(init?.method).toBe('POST');
  expect(init?.body).toBe(file);
  expect((init?.headers as Record<string, string>)['Content-Type']).toBe('application/zip');
});

it('throws what is not a refusal: a solution with problems, or no right to install', async () => {
  setToken('token');
  vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
    new Response(JSON.stringify({ code: 'validation_failed', detail: 'the solution has problems' }), { status: 422 }),
  );
  await expect(sendSolution('w1', new Blob(['zip']), 'plan', {})).rejects.toThrow('the solution has problems');
  vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
    new Response(JSON.stringify({ code: 'conflict', detail: 'something else' }), { status: 409 }),
  );
  await expect(sendSolution('w1', new Blob(['zip']), 'apply', {})).rejects.toThrow('something else');
});

it('leaves a paused installation paused unless the box was changed', () => {
  expect(optionsOf(noChoices).paused).toBeUndefined();
  expect(JSON.parse(JSON.stringify(optionsOf(noChoices)))).not.toHaveProperty('paused');
  expect(optionsOf({ ...noChoices, paused: false }).paused).toBe(false);
});
