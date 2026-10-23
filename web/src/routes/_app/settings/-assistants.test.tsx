// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ComponentType } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type * as Auth from '@/api/auth';
import { clientSetup, Route } from './assistants';

vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options }),
}));

const auth = vi.hoisted(() => ({ endpoint: 'https://mcp.example/v1/mcp' as string | null }));
vi.mock('@/api/auth', async (original) => ({
  ...(await original<typeof Auth>()),
  mcpEndpoint: () => auth.endpoint,
}));

beforeEach(() => {
  auth.endpoint = 'https://mcp.example/v1/mcp';
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const workspace = { id: 'w1', kind: 'workspace', name: 'Operations' };
const lists = [
  { id: 'l1', kind: 'list', name: 'Invoices' },
  { id: 'l2', kind: 'library', name: 'Contracts' },
];
const reading = ['workspace.read', 'list.read', 'contentType.read', 'document.read', 'taxonomy.read', 'user.read'];

interface Served {
  posts: unknown[];
  checks: { authorization: string | null }[];
  deleted: string[];
}

/** Serves the API, and the MCP endpoint answering with status. */
function serve(granted: string[], { existing = [] as unknown[], mcpStatus = 200 } = {}): Served {
  const served: Served = { posts: [], checks: [], deleted: [] };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      if (String(input) === auth.endpoint) {
        served.checks.push({ authorization: new Headers(init.headers).get('Authorization') });
        return new Response(
          JSON.stringify({ jsonrpc: '2.0', id: 1, result: { serverInfo: { name: 'papergo-mcp' } } }),
          {
            status: mcpStatus,
          },
        );
      }
      const path = (input as URL).pathname;
      if (init.method === 'DELETE') {
        served.deleted.push(path);
        return new Response(null, { status: 204 });
      }
      if (init.method === 'POST') {
        served.posts.push(JSON.parse(String(init.body)));
        return new Response(
          JSON.stringify({ token: { id: 'new', name: 'AI assistant' }, secret: 'pgo_assistant_secret' }),
        );
      }
      const data = path.endsWith('/me/scopes')
        ? granted
        : path.endsWith('/me/tokens')
          ? existing
          : path === '/v1/workspaces'
            ? { value: [workspace], '@odata.nextLink': null }
            : path === '/v1/resources/w1/children'
              ? { value: lists, '@odata.nextLink': null }
              : path === '/v1/resources/l1'
                ? lists[0]
                : null;
      return data === null ? new Response('{}', { status: 404 }) : new Response(JSON.stringify(data));
    }),
  );
  return served;
}

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const Page = Route.options.component as ComponentType;
  render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  );
  return client;
}

it('connects a read-only assistant to everything, then checks the connection', async () => {
  const served = serve([...reading, 'list.write', 'workspace.manage']);
  const client = show();
  const summary = screen.getByLabelText('This connection');
  expect(summary.textContent).toContain('Read only');
  expect(summary.textContent).toContain('Everything you can access');
  const create = screen.getByRole('button', { name: 'Create connection' }) as HTMLButtonElement;
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.click(create);
  expect(await screen.findByText('The assistant endpoint answered with this token')).toBeTruthy();
  expect(served.posts).toEqual([{ name: 'AI assistant', scopes: reading, expires_in_days: 90 }]);
  expect(served.checks).toEqual([{ authorization: 'Bearer pgo_assistant_secret' }]);
  expect(screen.getByDisplayValue('https://mcp.example/v1/mcp')).toBeTruthy();
  expect((screen.getByLabelText('Claude Code command') as HTMLInputElement).value).toContain(
    'Authorization: Bearer pgo_assistant_secret',
  );
  client.clear();
});

it('reports an unreachable endpoint without hiding the created token', async () => {
  const served = serve(reading, { mcpStatus: 404 });
  const client = show();
  const create = screen.getByRole('button', { name: 'Create connection' }) as HTMLButtonElement;
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.click(create);
  expect(await screen.findByText(/Not reachable yet/)).toBeTruthy();
  expect(screen.getByText('Token created')).toBeTruthy();
  expect(screen.getByDisplayValue('pgo_assistant_secret')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
  await waitFor(() => expect(served.checks).toHaveLength(2));
  client.clear();
});

it('limits a read-and-write connection to named places', async () => {
  const served = serve([...reading, 'list.write']);
  const client = show();
  fireEvent.click(await screen.findByLabelText(/Read and write/));
  fireEvent.click(screen.getByLabelText(/Only selected/));
  const create = screen.getByRole('button', { name: 'Create connection' }) as HTMLButtonElement;
  // Limited, but nothing chosen yet.
  await waitFor(() => expect(create.disabled).toBe(true));
  fireEvent.click(await screen.findByRole('button', { name: "Show Operations's lists and libraries" }));
  fireEvent.click(await screen.findByLabelText(/Invoices/));
  fireEvent.click(screen.getByLabelText(/Contracts/));
  expect(screen.getByText('Invoices (Operations), Contracts (Operations)')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: /Advanced options/ }));
  fireEvent.change(screen.getByLabelText('Expires in days'), { target: { value: '30' } });
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.click(create);
  await screen.findByDisplayValue('pgo_assistant_secret');
  expect(served.posts).toEqual([
    { name: 'AI assistant', scopes: [...reading, 'list.write'], resources: ['l1', 'l2'], expires_in_days: 30 },
  ]);
  client.clear();
});

it('takes a whole workspace instead of its lists', async () => {
  const served = serve(reading);
  const client = show();
  fireEvent.click(screen.getByLabelText(/Only selected/));
  fireEvent.click(await screen.findByRole('button', { name: "Show Operations's lists and libraries" }));
  fireEvent.click(await screen.findByLabelText(/Invoices/));
  fireEvent.click(screen.getByLabelText('Operations'));
  expect((screen.getByLabelText(/Invoices/) as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Create connection' }));
  await screen.findByDisplayValue('pgo_assistant_secret');
  expect((served.posts[0] as { resources: string[] }).resources).toEqual(['w1']);
  client.clear();
});

it('offers read and write only to people who can write', async () => {
  serve(reading);
  const client = show();
  await waitFor(() => expect((screen.getByLabelText(/Read and write/) as HTMLInputElement).disabled).toBe(true));
  client.clear();
});

it('explains that connections need an MCP server when the deployment runs none', async () => {
  auth.endpoint = null;
  serve(reading);
  const client = show();
  expect(screen.getByText(/does not run an MCP server yet/)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Create connection' })).toBeNull();
  client.clear();
});

it('lists connections with their places, state, and asks before revoking', async () => {
  const token = {
    prefix: 'pgo_x',
    subject: 'a',
    integration: false,
    created_by: 'a',
    created_at: '2026-01-01T00:00:00Z',
  };
  const served = serve(reading, {
    existing: [
      {
        ...token,
        id: 'tok1',
        name: 'Claude',
        expires_at: '2999-01-01T00:00:00Z',
        scopes: reading,
        resources: ['l1', 'gone'],
      },
      { ...token, id: 'tok2', name: 'Old', expires_at: '2020-01-01T00:00:00Z', scopes: [...reading, 'list.write'] },
    ],
  });
  const client = show();
  expect(await screen.findByText(/Invoices, 1 place no longer available/)).toBeTruthy();
  expect(screen.getByText('Not used yet')).toBeTruthy();
  expect(screen.getByText('Expired')).toBeTruthy();
  expect(screen.getByText(/Read and write · Everything you can access/)).toBeTruthy();
  fireEvent.click(screen.getAllByRole('button', { name: 'Revoke' })[0]!);
  expect(served.deleted).toEqual([]);
  const dialog = await screen.findByRole('alertdialog');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));
  await waitFor(() => expect(served.deleted).toEqual(['/v1/identity/me/tokens/tok1']));
  client.clear();
});

it('gives each client its own setup with the token as a header', () => {
  const setup = clientSetup('https://mcp.example/v1/mcp', 'pgo_s');
  expect(JSON.parse(setup.cursor)).toEqual({
    mcpServers: { papergo: { url: 'https://mcp.example/v1/mcp', headers: { Authorization: 'Bearer pgo_s' } } },
  });
  expect(JSON.parse(setup.vscode).servers.papergo.type).toBe('http');
  expect(setup.claudeCode).toBe(
    'claude mcp add --transport http papergo https://mcp.example/v1/mcp --header "Authorization: Bearer pgo_s"',
  );
});
