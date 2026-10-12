import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { toast, Toaster } from 'sonner';
import { clearToken, isApiError } from './api/http';
import { loadAuthentication } from './api/auth';
import { problemMessage } from './lib/errors';
import { routeTree } from './routeTree.gen';
import './styles.css';

function onError(error: unknown) {
  if (isApiError(error, 401)) {
    clearToken();
    if (!window.location.pathname.startsWith('/login')) window.location.assign('/login');
  }
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      onError(error);
      if (!mutation.meta?.silent && !isApiError(error, 401)) toast.error(problemMessage(error));
    },
  }),
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: true,
      retry: (count, error) => count < 2 && !(isApiError(error) && error.status >= 400 && error.status < 500),
    },
  },
});

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
  scrollRestoration: true,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

declare module '@tanstack/react-query' {
  interface Register {
    mutationMeta: { silent?: boolean };
  }
}

async function start() {
  const root = createRoot(document.getElementById('root')!);
  try {
    await loadAuthentication();
  } catch {
    root.render(
      <main className="p-8">
        <h1 className="text-lg font-semibold">Unable to load sign-in settings</h1>
        <p className="mt-2">Refresh this page after checking your connection and proxy authentication.</p>
      </main>,
    );
    return;
  }
  root.render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        <Toaster richColors position="top-right" />
      </QueryClientProvider>
    </StrictMode>,
  );
}
void start();
