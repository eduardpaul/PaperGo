import { useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import { api, isApiError, setToken } from '@/api/http';
import type { Me } from '@/api/types';
import { Logo } from '@/components/brand/logo';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>) => ({
    returnTo: typeof search.returnTo === 'string' && search.returnTo.startsWith('/') ? search.returnTo : '/',
  }),
  component: Login,
});

function Login() {
  const { returnTo } = Route.useSearch();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    setPending(true);
    setError('');
    setToken(value.trim());
    try {
      await api<Me>('/v1/identity/me');
      queryClient.clear();
      void navigate({ href: returnTo });
    } catch (cause) {
      setToken('');
      setError(isApiError(cause) ? problemMessage(cause) : 'That token was not accepted.');
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center px-4">
      <form onSubmit={(event) => void onSubmit(event)} className="w-full max-w-sm rounded-xl border bg-surface p-6 shadow-xs">
        <Logo />
        <h1 className="mt-6 text-lg font-semibold">Sign in</h1>
        <p className="mt-1 text-[13px] text-muted">
          Paste a development bearer token or a personal REST token. PaperGo does not keep passwords in this app.
        </p>
        <Label htmlFor="token" className="mt-5 block">
          Bearer token
        </Label>
        <Input id="token" className="mt-1.5" type="password" autoComplete="off" value={value} onChange={(event) => setValue(event.target.value)} required />
        {error && <Alert className="mt-3">{error}</Alert>}
        <Button type="submit" variant="primary" className="mt-4 w-full" disabled={pending}>
          {pending ? 'Checking…' : 'Continue'}
        </Button>
      </form>
    </main>
  );
}
