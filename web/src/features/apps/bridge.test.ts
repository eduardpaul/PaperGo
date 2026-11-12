// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { setToken } from '@/api/http';
import { appChange, appDocument, refusal, rememberRead, send, type Places } from './bridge';

afterEach(() => {
  vi.restoreAllMocks();
});

it('refuses the routes that administer PaperGo, whoever views the app', () => {
  for (const [method, url] of [
    ['POST', '/v1/identity/me/tokens'],
    ['GET', '/v1/identity/users'],
    ['PATCH', '/v1/organization/preferences'],
    ['PUT', '/v1/organization/settings/trusted-hosts'],
    ['POST', '/v1/portability/exports'],
    ['GET', '/v1/audit'],
    ['POST', '/v1/$batch'],
    ['GET', '/v1/events'],
    ['POST', '/v1/events/s1/subscriptions'],
    ['GET', '/v1/connections?workspace=w1'],
    ['POST', '/v1/connections/c1/test'],
    ['PUT', '/v1/connections/c1'],
    ['PUT', '/v1/apps/a1'],
    ['POST', '/v1/workspaces/w1/apps'],
    ['GET', '/v1/%69dentity/users'],
    ['GET', '/v1/resources/../identity/users'],
    ['PUT', '/v1/workflows/wf1'],
    ['POST', '/v1/workspaces/w1/workflows'],
    ['PUT', '/v1/workspaces/w1/workflow-builtins/search.index'],
    ['POST', '/v1/workspaces/w1/solutions/apply'],
    ['POST', '/v1/workspaces/w1/solutions/plan'],
    ['DELETE', '/v1/workspaces/w1/solutions/diesel'],
    ['POST', '/v1/workspaces/w1/members'],
    ['DELETE', '/v1/workspaces/w1/members/bob'],
    ['PUT', '/v1/resources/r1/permissions'],
    ['POST', '/v1/resources/l1/integrations'],
    ['PATCH', '/v1/resources/l1/integrations/i1'],
    ['GET', 'https://example.com/v1/items'],
    ['GET', '//example.com/v1/items'],
    ['TRACE', '/v1/resources/r1'],
  ]) {
    expect(refusal(method, url), `${method} ${url}`).toBeDefined();
  }
  for (const [method, url] of [
    ['GET', '/v1/resources/l1'],
    ['POST', '/v1/resources/l1/query?$top=200'],
    ['POST', '/v1/resources/l1/query/aggregates'],
    ['POST', '/v1/resources/l1/query/compact'],
    ['POST', '/v1/resources/l1/children'],
    ['PATCH', '/v1/resources/i1'],
    ['DELETE', '/v1/resources/i1'],
    ['GET', '/v1/me/tasks?key=diesel_measure&status=open'],
    ['POST', '/v1/tasks/t1/responses'],
    ['GET', '/v1/workspaces/w1/workflows'],
    ['GET', '/v1/workspaces/w1/solutions/diesel/status'],
    ['POST', '/v1/workflows/wf1/runs'],
    ['GET', '/v1/workspaces/w1/members'],
    ['GET', '/v1/resources/l1/integrations'],
    ['GET', '/v1/workspaces/w1/connections/home'],
    ['POST', '/v1/workspaces/w1/connections/home/channels/lights/publish'],
    ['GET', '/v1/workspaces/w1/connections/nodered/channels/status/fetch?path=%2Fstatus'],
    ['POST', '/v1/workspaces/w1/connections/nodered/channels/heating/call'],
  ]) {
    expect(refusal(method, url), `${method} ${url}`).toBeUndefined();
  }
});

it('sends an app request as the viewer, with only the headers apps may set', async () => {
  setToken('viewer-token');
  const fetcher = vi.fn(async (_url: URL | RequestInfo, init?: RequestInit) => {
    void init;
    return new Response(JSON.stringify({ id: 'i1' }), { status: 201, headers: { 'Content-Type': 'application/json', ETag: '"1"', Location: '/v1/resources/i1', 'Set-Cookie': 'x=1' } });
  });
  const response = await send(
    { method: 'post', url: '/v1/resources/l1/children', headers: { 'content-type': 'application/json', authorization: 'Bearer stolen', cookie: 'a=b', 'if-match': '"3"' }, body: '{"name":"Reading"}' },
    fetcher as unknown as typeof fetch,
  );
  expect(response).toEqual({ status: 201, headers: { 'content-type': 'application/json', etag: '"1"', location: '/v1/resources/i1' }, body: '{"id":"i1"}' });
  const [url, init] = fetcher.mock.calls[0];
  expect(String(url)).toBe(`${window.location.origin}/v1/resources/l1/children`);
  expect(init).toMatchObject({ method: 'POST', body: '{"name":"Reading"}', credentials: 'same-origin', redirect: 'error' });
  expect(init?.headers).toEqual({ 'content-type': 'application/json', 'if-match': '"3"', Authorization: 'Bearer viewer-token', 'papergo-app': '1' });

  const refused = await send({ method: 'POST', url: '/v1/identity/me/tokens', headers: {} }, fetcher as unknown as typeof fetch);
  expect(refused.status).toBe(403);
  expect(JSON.parse(refused.body).code).toBe('app_refused');
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it('writes a frame document whose scripts cannot be ended early and that loads nothing', () => {
  const html = appDocument('var runtime = "</script><img src=x>";', 'var app = 1; // </SCRIPT>', { app: { name: '</script><b>' } });
  expect(html).toContain(`content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; webrtc 'block'; form-action 'none'; base-uri 'none'"`);
  expect(html.match(/<script>/g)).toHaveLength(3);
  expect(html.match(/<\/script>/gi)).toHaveLength(3);
  expect(html).toContain('"\\u003c/script>\\u003cb>"');
});

it('keeps a <!-- in a script from swallowing the scripts after it', () => {
  const html = appDocument('var runtime = 1;', 'var text = "<!-- <script>";', {});
  expect(html).not.toContain('<!--');
  expect(html).toContain('"\\x3C!-- <script>"');
});

it('keeps what a <!-- in a script means, in a string and in a regular expression', () => {
  const code = 'globalThis.read = ["<!-- a", /<!--/u.test("<!--"), /a<!--/v.test("a<!--")];';
  const written = appDocument('', code, {}).split('<script>')[2].split('</script>')[0];
  expect(written).not.toContain('<!--');
  const read = new Function(`${written}; return globalThis.read;`)() as unknown[];
  expect(read).toEqual(['<!-- a', true, true]);
});

it('tells an app of the changes of its own workspace only', () => {
  const places: Places = new Map();
  const change = { id: 'i1', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't1', removed: false };
  expect(appChange(change, 'w1', places)).toEqual({ id: 'i1', collectionId: 'l1', contentTypeId: 't1', removed: false });
  expect(appChange({ id: 'i3', workspaceId: 'w2', collectionId: 'l9' }, 'w1', places)).toBeUndefined();
  expect(appChange({ id: 'i2', workspaceId: 'w2', removed: true }, 'w1', places)).toBeUndefined();
  expect(appChange({ id: '', resync: true }, 'w1', places)).toEqual({ id: '', resync: true });
});

it('tells an app where an item was, and of one that left its workspace', () => {
  const places: Places = new Map();
  rememberRead('/v1/resources/l1/query/compact?$top=500', { status: 200, headers: {}, body: JSON.stringify({ value: [{ id: 'i1', workspace_id: 'w1', content_type_id: 't1' }, { id: 'x', workspace_id: 'w2' }] }) }, 'w1', places);
  expect(places.get('i1')).toEqual({ collectionId: 'l1', contentTypeId: 't1' });
  expect(places.has('x')).toBe(false);
  expect(appChange({ id: 'i1', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't2' }, 'w1', places)).toEqual({ id: 'i1', collectionId: 'l1', contentTypeId: 't2', from: { collectionId: 'l1', contentTypeId: 't1' } });
  // Moved to another workspace: removed from where the app saw it, without saying where it went.
  expect(appChange({ id: 'i1', workspaceId: 'w2', collectionId: 'l9', contentTypeId: 't9' }, 'w1', places)).toEqual({ id: 'i1', collectionId: 'l1', contentTypeId: 't2', removed: true });
  expect(appChange({ id: 'i1', workspaceId: 'w2', collectionId: 'l9' }, 'w1', places)).toBeUndefined();
});
