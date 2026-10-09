// End-to-end tests of the built SDK against a real API (test/server): run with `npm test`.
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { createClient, ifMatch, isProblem } from '../dist/index.js';

const token = randomBytes(16).toString('hex');
const root = fileURLToPath(new URL('../../..', import.meta.url));
let baseUrl;
let bin;
let server;
let api;

before(async () => {
  bin = await mkdtemp(join(tmpdir(), 'papergo-sdk-test-'));
  const binary = join(bin, process.platform === 'win32' ? 'server.exe' : 'server');
  await promisify(execFile)('go', ['build', '-o', binary, './sdk/typescript/test/server'], { cwd: root });
  server = spawn(binary, ['-token', token], { cwd: root, stdio: ['ignore', 'pipe', 'inherit'] });
  const exited = new Promise((_, reject) => server.once('exit', (code) => reject(new Error(`test server exited with ${code}`))));
  [baseUrl] = await Promise.race([createInterface({ input: server.stdout })[Symbol.asyncIterator]().next().then((line) => [line.value]), exited]);
  api = createClient({ baseUrl, token });
}, { timeout: 180_000 });

after(async () => {
  server?.kill('SIGTERM');
  await rm(bin, { recursive: true, force: true });
});

test('health is public', async () => {
  const anonymous = createClient({ baseUrl, token: '' });
  assert.equal((await anonymous.health.ready.get())?.status, 'ok');
  await assert.rejects(anonymous.v1.workspaces.get(), (error) => isProblem(error, 'unauthorized') && error.responseStatusCode === 401);
});

test('resources, fields, values and preconditions', async () => {
  const workspace = await api.v1.workspaces.post({ name: 'SDK', tags: ['sdk'] });
  assert.ok(workspace?.id);
  const page = await api.v1.workspaces.get({ queryParameters: { limit: 10 } });
  assert.ok(page?.data?.some((w) => w.id === workspace.id));

  const list = await api.v1.resources.byId(workspace.id).children.post({ kind: 'list', name: 'Invoices' });
  await api.v1.resources.byId(list.id).fields.post({ key: 'status', label: 'Status', type: 'choice', choices: ['open', 'paid'], indexed: true });
  await api.v1.resources.byId(list.id).fields.post({ key: 'amount', label: 'Amount', type: 'decimal', scale: 2 });
  const item = await api.v1.resources.byId(list.id).children.post({ kind: 'item', name: 'INV-1', values: { additionalData: { status: 'open', amount: '12.50' } } });
  assert.deepEqual(item?.values?.additionalData, { status: 'open', amount: '12.50' });

  const renamed = await api.v1.resources.byId(item.id).patch({ name: 'INV-001' }, ifMatch(item.version));
  assert.equal(renamed?.name, 'INV-001');
  assert.equal(renamed?.version, item.version + 1);
  await assert.rejects(api.v1.resources.byId(item.id).patch({ name: 'stale' }, ifMatch(item.version)), (error) => isProblem(error, 'conflict') && error.responseStatusCode === 409);
  await assert.rejects(api.v1.resources.byId(item.id).patch({ name: 'unconditional' }), (error) => isProblem(error, 'precondition_required'));
  await assert.rejects(api.v1.resources.byId(list.id).children.post({ kind: 'item', name: 'INV-2', values: { additionalData: { status: 'void' } } }), (error) => isProblem(error, 'validation_failed') && error.responseStatusCode === 422);
  await assert.rejects(api.v1.resources.byId('00000000-0000-7000-8000-000000000000').get(), (error) => isProblem(error, 'not_found'));

  const result = await api.v1.resources.byId(list.id).query.post({ filter: { field: 'status', op: 'eq', value: 'open' } });
  assert.deepEqual(result?.data?.map((r) => r.id), [item.id]);
});

test('bulk operations report the failing index', async () => {
  const workspace = await api.v1.workspaces.post({ name: 'Bulk' });
  const list = await api.v1.resources.byId(workspace.id).children.post({ kind: 'list', name: 'Tasks' });
  const created = await api.v1.resources.byId(list.id).bulk.post({ operations: [{ action: 'create', create: { name: 'A' } }, { action: 'create', create: { name: 'B' } }] });
  assert.deepEqual(created?.data?.map((r) => r.action), ['create', 'create']);
  await assert.rejects(api.v1.resources.byId(list.id).bulk.post({ operations: [{ action: 'create', create: { name: 'C' } }, { action: 'delete', id: created.data[0].id, version: 99 }] }), (error) => isProblem(error, 'conflict') && error.operationIndex === 1);
});

test('content uploads and downloads bytes', async () => {
  const workspace = await api.v1.workspaces.post({ name: 'Files' });
  const library = await api.v1.resources.byId(workspace.id).children.post({ kind: 'library', name: 'Contracts' });
  const item = await api.v1.resources.byId(library.id).children.post({ kind: 'item', name: 'contract.txt' });
  const bytes = new TextEncoder().encode('signed contract');
  const blob = await api.v1.items.byId(item.id).content.put(bytes.buffer, 'text/plain', { headers: { ...ifMatch(item.version).headers, 'X-Filename': 'contract.txt' } });
  assert.equal(blob?.size, bytes.length);
  assert.equal(blob?.contentType, 'text/plain');
  const downloaded = await api.v1.items.byId(item.id).content.get();
  assert.equal(new TextDecoder().decode(downloaded), 'signed contract');
});
