import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useEffect, useState, type FormEvent } from 'react';
import { api, isApiError, setToken } from '@/api/http';
import { authenticationMode, oidcSignIn } from '@/api/auth';
import { safeReturnPath } from '@/api/oidc';
import { meQuery } from '@/api/queries';
import type { Me } from '@/api/types';
import { Logo } from '@/components/brand/logo';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>) => ({ returnTo: safeReturnPath(search.returnTo) }),
  component: Login,
});

function Login() {
  if (authenticationMode() === 'proxy') return <ProxyLogin />;
  return oidcSignIn() ? <ProviderLogin /> : <BearerLogin />;
}

function ProviderLogin() {
  const { returnTo } = Route.useSearch();
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [tokenForm, setTokenForm] = useState(false);
  const signIn = oidcSignIn()!;
  const provider = new URL(signIn.settings.issuer).host;

  async function onSignIn() {
    setPending(true);
    setError('');
    try {
      await signIn.signIn(returnTo);
    } catch {
      setPending(false);
      setError(`Unable to reach ${provider}. Check your connection and try again.`);
    }
  }

  if (tokenForm) return <BearerLogin />;
  return (
    <main className="flex min-h-dvh items-center justify-center px-4">
      <div className="w-full max-w-sm rounded-xl border bg-surface p-6 shadow-xs">
        <Logo />
        <h1 className="mt-6 text-lg font-semibold">Sign in</h1>
        <p className="mt-1 text-[13px] text-muted">Continue at your organization's identity provider, {provider}.</p>
        {error && <Alert className="mt-3">{error}</Alert>}
        <Button variant="primary" className="mt-5 w-full" disabled={pending} onClick={() => void onSignIn()}>
          {pending ? 'Redirecting…' : 'Sign in with single sign-on'}
        </Button>
        <Button variant="ghost" className="mt-2 w-full" onClick={() => setTokenForm(true)}>
          Use a personal REST token
        </Button>
      </div>
    </main>
  );
}

function ProxyLogin() {
  const { returnTo } = Route.useSearch();
  const navigate = useNavigate();
  const me = useQuery({ ...meQuery, retry: false });
  useEffect(() => {
    if (me.data) void navigate({ href: returnTo });
  }, [me.data, navigate, returnTo]);
  return (
    <main className="flex min-h-dvh items-center justify-center px-4">
      <div className="w-full max-w-sm rounded-xl border bg-surface p-6">
        <Logo />
        <h1 className="mt-6 text-lg font-semibold">Proxy sign-in</h1>
        <p className="mt-2 text-sm">
          Your reverse proxy manages authentication. Use its browser sign-in prompt to access PaperGo.
        </p>
        {me.error && <Alert className="mt-3">{problemMessage(me.error)}</Alert>}
        <Button className="mt-4" disabled={me.isFetching} onClick={() => void me.refetch()}>
          Check access again
        </Button>
      </div>
    </main>
  );
}

function BearerLogin() {
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
      <form
        onSubmit={(event) => void onSubmit(event)}
        className="w-full max-w-sm rounded-xl border bg-surface p-6 shadow-xs"
      >
        <Logo />
        <h1 className="mt-6 text-lg font-semibold">Sign in</h1>
        <p className="mt-1 text-[13px] text-muted">
          {oidcSignIn()
            ? 'Paste a personal REST token. PaperGo does not keep passwords in this app.'
            : 'Paste a development bearer token or a personal REST token. PaperGo does not keep passwords in this app.'}
        </p>
        <Label htmlFor="token" className="mt-5 block">
          Bearer token
        </Label>
        <Input
          id="token"
          className="mt-1.5"
          type="password"
          autoComplete="off"
          value={value}
          onChange={(event) => setValue(event.target.value)}
          required
        />
        {error && <Alert className="mt-3">{error}</Alert>}
        <Button type="submit" variant="primary" className="mt-4 w-full" disabled={pending}>
          {pending ? 'Checking…' : 'Continue'}
        </Button>
      </form>
    </main>
  );
}
