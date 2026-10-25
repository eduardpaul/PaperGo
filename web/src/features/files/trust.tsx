import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, ImageOff } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { trustedHostsQuery } from '@/api/queries';
import type { TrustedHosts } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { useHasScope } from '@/features/admin/common';
import { useBlobUrl } from '@/lib/use-blob-url';

/** Whether hosts trust url: this app's own origin, or a web URL on a listed host. */
export function trusts(hosts: string[] | undefined, url: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(url, window.location.href);
  } catch {
    return false;
  }
  if (parsed.origin === window.location.origin) return true;
  if (!hosts || (parsed.protocol !== 'https:' && parsed.protocol !== 'http:')) return false;
  return matches(hosts, parsed.hostname);
}

/** Whether host is an IP address. The browser cannot tell a public address from a private one, so none is trusted here. */
const isAddress = (host: string) => /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.startsWith('[');

/** Whether a trusted host list names host: *, the host itself, or a wildcard for a domain above it. */
export function matches(hosts: string[], host: string): boolean {
  const name = host.toLowerCase().replace(/\.$/, '');
  if (isAddress(name)) return false;
  return hosts.some((entry) => entry === '*' || entry === name || (entry.startsWith('*.') && name.endsWith(entry.slice(1))));
}

/**
 * Whether the server's fetches, such as calendar integrations, may reach
 * host under a trusted host list. Unlike the browser, the server knows a
 * public address from a private one, so * trusts IP addresses there too.
 */
export function serverMatches(hosts: string[], host: string): boolean {
  return isAddress(host.toLowerCase()) ? hosts.includes('*') : matches(hosts, host);
}

/** The host a URL names, for messages; empty for what is not a web URL. */
export function hostOf(url: string): string {
  try {
    const parsed = new URL(url, window.location.href);
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed.hostname : '';
  } catch {
    return '';
  }
}

/**
 * The organization's trusted hosts, and the check every handler uses. Until
 * the list loads nothing outside the app counts as trusted.
 */
export function useTrust() {
  const hosts = useQuery(trustedHostsQuery);
  return { hosts: hosts.data?.hosts, trusted: (url: string) => trusts(hosts.data?.hosts, url) };
}

/** Adds a host to the trusted list, for administrators, from where it was needed. */
export function useTrustHost() {
  const client = useQueryClient();
  const allowed = useHasScope('organization.manage');
  const trust = useMutation({
    mutationFn: async (host: string) => {
      const current = await client.fetchQuery({ ...trustedHostsQuery, staleTime: 0 });
      return (await api<TrustedHosts>('/v1/organization/settings/trusted-hosts', { method: 'PUT', ifMatch: current.version, body: { hosts: [...current.hosts, host] } })).data;
    },
    onSuccess: (hosts, host) => {
      client.setQueryData(trustedHostsQuery.queryKey, hosts);
      toast.success(`${host} is trusted now`);
    },
  });
  return { allowed, trust };
}

const linkStyle = 'cursor-pointer text-accent underline underline-offset-2 hover:no-underline';

/**
 * A link out of a document. Links to trusted hosts open in a new tab; others
 * ask first, naming the host and the whole address. Addresses that are not
 * web links are shown as text. An untrusted link carries no address the
 * browser could open by itself, so middle clicks, modifier clicks and the
 * context menu's new tab ask too, or do nothing.
 */
export function TrustedLink({ href, children }: { href?: string; children?: ReactNode }) {
  const { trusted } = useTrust();
  const [asking, setAsking] = useState(false);
  const host = href ? hostOf(href) : '';
  if (!href || !host) return <span>{children}</span>;
  if (trusted(href))
    return (
      <a href={href} target="_blank" rel="noopener noreferrer nofollow" className={linkStyle}>
        {children}
      </a>
    );
  const ask = (event: React.SyntheticEvent) => {
    event.preventDefault();
    setAsking(true);
  };
  return (
    <>
      <a
        role="link"
        tabIndex={0}
        title={href}
        className={linkStyle}
        onClick={ask}
        onAuxClick={(event) => event.button === 1 && ask(event)}
        onKeyDown={(event) => event.key === 'Enter' && ask(event)}
      >
        {children}
      </a>
      {asking && <LeavingDialog href={href} host={host} onClose={() => setAsking(false)} />}
    </>
  );
}

/**
 * Follows links from content PaperGo does not render itself, such as a Word
 * document in a sandboxed frame: trusted hosts open in a new tab, others ask
 * first. Render dialog wherever the links are.
 */
export function useFollowLink() {
  const { trusted } = useTrust();
  const [asking, setAsking] = useState<{ href: string; host: string }>();
  const follow = (href: string) => {
    const host = hostOf(href);
    if (!host) return;
    if (trusted(href)) window.open(href, '_blank', 'noopener,noreferrer');
    else setAsking({ href, host });
  };
  const dialog = asking ? <LeavingDialog href={asking.href} host={asking.host} onClose={() => setAsking(undefined)} /> : null;
  return { follow, dialog };
}

function LeavingDialog({ href, host, onClose }: { href: string; host: string; onClose: () => void }) {
  const { allowed, trust } = useTrustHost();
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>You're leaving PaperGo for {host}</DialogTitle>
          <DialogDescription>This link goes to a site your organization has not marked as trusted. Check the address before you open it.</DialogDescription>
        </DialogHeader>
        <p className="mx-5 rounded-md bg-surface-muted px-3 py-2 font-mono text-xs break-all">{href}</p>
        <DialogFooter>
          {allowed && !isAddress(host) && (
            <Button className="mr-auto" variant="ghost" disabled={trust.isPending} onClick={() => trust.mutate(host)}>
              Trust {host}
            </Button>
          )}
          <Button onClick={onClose}>Cancel</Button>
          <Button asChild variant="primary">
            <a href={href} target="_blank" rel="noopener noreferrer nofollow" onClick={onClose}>
              <ExternalLink /> Open link
            </a>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The path of a URL on this app's own origin, which needs the app's credentials; undefined for other origins. */
function ownPath(src: string | undefined) {
  if (!src) return undefined;
  try {
    const url = new URL(src, window.location.href);
    return url.origin === window.location.origin ? url.pathname + url.search : undefined;
  } catch {
    return undefined;
  }
}

/**
 * An image a document loads from elsewhere. Images from trusted hosts load;
 * others wait for Show once (or Show all images in this file), so opening a
 * document never tells another site who read it. Images stored in PaperGo
 * are fetched with the reader's credentials, as with a bearer token the
 * browser would not send them by itself.
 */
export function RemoteImage({ src, alt, shown, onShow }: { src?: string; alt?: string; shown: boolean; onShow: (all: boolean) => void }) {
  const { trusted } = useTrust();
  const { allowed, trust } = useTrustHost();
  const [once, setOnce] = useState(false);
  const own = ownPath(src);
  const stored = useBlobUrl(own);
  const host = src ? hostOf(src) : '';
  if (own) {
    if (stored.url) return <img src={stored.url} alt={alt ?? ''} loading="lazy" className="max-w-full rounded" />;
    return <span className="text-muted">[{stored.error ? `${alt || 'image'} could not be loaded` : alt || 'image'}]</span>;
  }
  if (!src || !host) return <span className="text-muted">[{alt || 'image'}]</span>;
  if (once || shown || trusted(src)) return <img src={src} alt={alt ?? ''} referrerPolicy="no-referrer" loading="lazy" className="max-w-full rounded" />;
  return (
    <span role="note" className="my-2 flex flex-wrap items-center gap-2 rounded-md border border-dashed px-3 py-2 text-xs text-muted">
      <ImageOff className="size-4" />
      <span>
        Image from <strong>{host}</strong> not shown{alt ? `: ${alt}` : ''}.
      </span>
      <Button size="sm" variant="ghost" onClick={() => setOnce(true)}>
        Show once
      </Button>
      <Button size="sm" variant="ghost" onClick={() => onShow(true)}>
        Show all images in this file
      </Button>
      {allowed && !isAddress(host) && (
        <Button size="sm" variant="ghost" disabled={trust.isPending} onClick={() => trust.mutate(host)}>
          Trust {host}
        </Button>
      )}
    </span>
  );
}
