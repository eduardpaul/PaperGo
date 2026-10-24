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
  const host = parsed.hostname.toLowerCase();
  return hosts.some((entry) => entry === '*' || entry === host || (entry.startsWith('*.') && host.endsWith(entry.slice(1))));
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

/**
 * A link out of a document. Links to trusted hosts open in a new tab; others
 * ask first, naming the host and the whole address. Addresses that are not
 * web links are shown as text.
 */
export function TrustedLink({ href, children }: { href?: string; children?: ReactNode }) {
  const { trusted } = useTrust();
  const [asking, setAsking] = useState(false);
  const host = href ? hostOf(href) : '';
  if (!href || !host) return <span>{children}</span>;
  return (
    <>
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer nofollow"
        className="text-accent underline underline-offset-2 hover:no-underline"
        onClick={(event) => {
          if (trusted(href)) return;
          event.preventDefault();
          setAsking(true);
        }}
      >
        {children}
      </a>
      {asking && <LeavingDialog href={href} host={host} onClose={() => setAsking(false)} />}
    </>
  );
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
          {allowed && (
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

/**
 * An image a document loads from elsewhere. Images from trusted hosts load;
 * others wait for Show once (or Show all images in this file), so opening a
 * document never tells another site who read it.
 */
export function RemoteImage({ src, alt, shown, onShow }: { src?: string; alt?: string; shown: boolean; onShow: (all: boolean) => void }) {
  const { trusted } = useTrust();
  const { allowed, trust } = useTrustHost();
  const [once, setOnce] = useState(false);
  const host = src ? hostOf(src) : '';
  if (!src || (!host && !src.startsWith('/'))) return <span className="text-muted">[{alt || 'image'}]</span>;
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
      {allowed && (
        <Button size="sm" variant="ghost" disabled={trust.isPending} onClick={() => trust.mutate(host)}>
          Trust {host}
        </Button>
      )}
    </span>
  );
}
