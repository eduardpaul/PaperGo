import { useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { oidcSignIn } from '@/api/auth';
import { Logo } from '@/components/brand/logo';
import { Alert } from '@/components/ui/feedback';
import { useTranslation } from '@/i18n';

/** The OIDC redirect URI: completes provider sign-in, then continues where sign-in started. */
export const Route = createFileRoute('/login_/callback')({
  component: Callback,
});

function Callback() {
  const { t } = useTranslation('auth');
  const queryClient = useQueryClient();
  const signIn = oidcSignIn();
  const [error, setError] = useState(signIn ? '' : t('callback.noProvider'));
  const started = useRef(false);
  useEffect(() => {
    // The authorization code is single-use; StrictMode must not redeem it twice.
    if (started.current || !signIn) return;
    started.current = true;
    signIn
      .complete()
      .then((returnTo) => {
        queryClient.clear();
        // A full navigation replaces the callback URL, which carries the one-time code.
        window.location.replace(returnTo);
      })
      .catch(() => setError(t('callback.expired')));
  }, [queryClient, signIn, t]);

  return (
    <main className="flex min-h-dvh items-center justify-center px-4">
      <div className="w-full max-w-sm rounded-xl border bg-surface p-6 shadow-xs">
        <Logo />
        {error ? (
          <>
            <h1 className="mt-6 text-lg font-semibold">{t('callback.failed')}</h1>
            <Alert className="mt-3">{error}</Alert>
            <Link to="/login" search={{ returnTo: '/' }} className="mt-4 inline-block text-accent hover:underline">
              {t('callback.tryAgain')}
            </Link>
          </>
        ) : (
          <h1 className="mt-6 text-lg font-semibold">{t('callback.signingIn')}</h1>
        )}
      </div>
    </main>
  );
}
