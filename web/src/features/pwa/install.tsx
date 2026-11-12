import { Download } from 'lucide-react';
import { useEffect, useState, useSyncExternalStore } from 'react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { lookQuery, iconUrl, type AppLook } from '@/features/apps/icon';
import { useTranslation } from '@/i18n';
import { standalone } from './installed';

/** The browser's offer to install what the page's manifest describes. */
interface InstallPrompt extends Event {
  prompt(): Promise<unknown>;
}

let offer: InstallPrompt | null = null;
const listeners = new Set<() => void>();

function setOffer(next: InstallPrompt | null) {
  offer = next;
  for (const listener of listeners) listener();
}

/** Keeps the browser's install offer for the Install button, instead of the browser's own banner. */
export function watchInstallOffer(): void {
  window.addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault();
    setOffer(event as InstallPrompt);
  });
  window.addEventListener('appinstalled', () => setOffer(null));
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const paperGo = { manifest: '/pwa/papergo.webmanifest', icon: '/pwa/icon-180.png', title: 'PaperGo' };

// What the page offers to install is what its manifest link names. An offer
// made for the manifest before no longer holds once the link changes; the
// browser makes a new one.
function point(to: typeof paperGo) {
  setOffer(null);
  document.querySelector('link[rel="manifest"]')?.setAttribute('href', to.manifest);
  document.querySelector('link[rel="apple-touch-icon"]')?.setAttribute('href', to.icon);
  document.querySelector('meta[name="apple-mobile-web-app-title"]')?.setAttribute('content', to.title);
  document.title = to.title;
}

/** The address of the manifest that installs an app as its own home-screen app. */
export function appManifestUrl(app: AppLook & { id: string; workspace_id: string }): string {
  const query = lookQuery(app);
  query.set('workspace', app.workspace_id);
  query.set('app', app.id);
  return `/pwa/app.webmanifest?${query}`;
}

/** While an app's page shows, installing the page installs that app; elsewhere it installs PaperGo. */
export function useAppManifest(app: (AppLook & { id: string; workspace_id: string }) | undefined): void {
  const manifest = app && appManifestUrl(app);
  const icon = app && iconUrl(app, 'icon-180.png');
  const title = app?.name;
  useEffect(() => {
    if (!manifest || !icon || !title) return;
    point({ manifest, icon, title });
    return () => point(paperGo);
  }, [manifest, icon, title]);
}

function appleTouch(): boolean {
  return /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

/**
 * Installs what the page describes, PaperGo or the app being shown, on this
 * device. It shows where the browser offers to install, and on iPhone and
 * iPad, which have no offer, explains the Share menu. An installed window
 * has no use for it.
 */
export function InstallButton({ name, compact }: { name: string; compact?: boolean }) {
  const { t } = useTranslation('apps');
  const prompt = useSyncExternalStore(subscribe, () => offer);
  const [explaining, setExplaining] = useState(false);
  const apple = !prompt && appleTouch();
  if (standalone() || (!prompt && !apple)) return null;
  const label = t('install.action', { name });
  return (
    <>
      <Button
        size={compact ? 'icon' : undefined}
        variant={compact ? 'ghost' : undefined}
        className={compact ? 'size-10' : undefined}
        aria-label={label}
        title={label}
        onClick={() => {
          if (prompt) void prompt.prompt().finally(() => setOffer(null));
          else setExplaining(true);
        }}
      >
        <Download />
        {!compact && t('install.short')}
      </Button>
      <Dialog open={explaining} onOpenChange={setExplaining}>
        <DialogContent>
          <DialogHeader className="pb-5">
            <DialogTitle>{label}</DialogTitle>
            <DialogDescription>{t('install.appleSteps')}</DialogDescription>
          </DialogHeader>
        </DialogContent>
      </Dialog>
    </>
  );
}
