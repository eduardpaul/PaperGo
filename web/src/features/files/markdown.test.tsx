// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type * as Http from '@/api/http';
import { outline, Rendered } from './handlers/markdown';

vi.mock('@/api/http', async (actual) => ({ ...(await actual<typeof Http>()), api: vi.fn(() => new Promise(() => {})) }));
afterEach(cleanup);

it('keeps heading ids matching the outline across renders', () => {
  const source = '# Intro\n\nText.\n\n## Intro\n\n## Steps & notes\n\n```\n# not a heading\n```\n';
  const client = new QueryClient();
  const view = (className: string) => (
    <QueryClientProvider client={client}>
      <Rendered source={source} className={className} />
    </QueryClientProvider>
  );
  const { container, rerender } = render(view('a'));
  rerender(view('b'));
  rerender(view('a'));
  const ids = [...container.querySelectorAll('h1, h2, h3')].map((heading) => heading.id);
  expect(ids).toEqual(['intro', 'intro-1', 'steps-notes']);
  expect(outline(source).map((heading) => heading.id)).toEqual(ids);
});
