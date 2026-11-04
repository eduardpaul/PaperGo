// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { setToken } from '@/api/http';
import { appDocument, refusal, send } from './bridge';

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
    ['PUT', '/v1/apps/a1'],
    ['POST', '/v1/workspaces/w1/apps'],
    ['GET', '/v1/%69dentity/users'],
    ['GET', '/v1/resources/../identity/users'],
    ['PUT', '/v1/workflows/wf1'],
    ['POST', '/v1/workspaces/w1/workflows'],
    ['PUT', '/v1/workspaces/w1/workflow-builtins/search.index'],
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
    ['POST', '/v1/resources/l1/children'],
    ['PATCH', '/v1/resources/i1'],
    ['DELETE', '/v1/resources/i1'],
    ['GET', '/v1/me/tasks?key=diesel_measure&status=open'],
    ['POST', '/v1/tasks/t1/responses'],
    ['GET', '/v1/workspaces/w1/workflows'],
    ['POST', '/v1/workflows/wf1/runs'],
    ['GET', '/v1/workspaces/w1/members'],
    ['GET', '/v1/resources/l1/integrations'],
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
  expect(init?.headers).toEqual({ 'content-type': 'application/json', 'if-match': '"3"', Authorization: 'Bearer viewer-token' });

  const refused = await send({ method: 'POST', url: '/v1/identity/me/tokens', headers: {} }, fetcher as unknown as typeof fetch);
  expect(refused.status).toBe(403);
  expect(JSON.parse(refused.body).code).toBe('app_refused');
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it('writes a frame document whose scripts cannot be ended early and that loads nothing', () => {
  const html = appDocument('var runtime = "</script><img src=x>";', 'var app = 1; // </SCRIPT>', { app: { name: '</script><b>' } });
  expect(html).toContain(`content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'"`);
  expect(html.match(/<script>/g)).toHaveLength(3);
  expect(html.match(/<\/script>/gi)).toHaveLength(3);
  expect(html).toContain('"\\u003c/script>\\u003cb>"');
});
