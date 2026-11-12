import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { AppWindow, LayoutGrid, Search } from 'lucide-react';
import { useState } from 'react';
import { Page, PageHeader } from '@/components/page';
import { Badge } from '@/components/ui/badge';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { QueryFeedback } from '@/features/admin/common';
import { launcherQuery, type App } from '@/features/apps/api';
import { AppIcon } from '@/features/apps/icon';
import { InstallButton } from '@/features/pwa/install';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/launcher')({ component: LauncherPage });

const normalized = (text: string) => text.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

/**
 * Every app the viewer may open, in all their workspaces, grouped by
 * workspace. Typing narrows them by name, description or workspace, and
 * Enter opens the first.
 */
function LauncherPage() {
  const { t } = useTranslation('apps');
  const navigate = useNavigate();
  const apps = useQuery(launcherQuery);
  const [q, setQ] = useState('');
  const words = normalized(q.trim()).split(/\s+/).filter(Boolean);
  const shown = (apps.data ?? []).filter((app) => {
    const text = normalized(`${app.name} ${app.description} ${app.workspace_name ?? ''}`);
    return words.every((word) => text.includes(word));
  });
  const groups = new Map<string, { name: string; apps: App[] }>();
  for (const app of shown) {
    const group = groups.get(app.workspace_id) ?? { name: app.workspace_name ?? '', apps: [] };
    group.apps.push(app);
    groups.set(app.workspace_id, group);
  }
  const sorted = [...groups.entries()].sort(([, a], [, b]) => a.name.localeCompare(b.name));
  const open = (app: App) => void navigate({ to: '/w/$workspaceId/a/$appId', params: { workspaceId: app.workspace_id, appId: app.id } });

  return (
    <Page>
      <PageHeader title={t('launcher.title')} description={t('launcher.description')} icon={LayoutGrid} actions={<InstallButton name="PaperGo" />} />
      <QueryFeedback query={apps} />
      {apps.isPending && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      )}
      {apps.data?.length === 0 && (
        <EmptyState icon={AppWindow} title={t('launcher.empty')}>
          {t('launcher.emptyHint')}
        </EmptyState>
      )}
      {!!apps.data?.length && (
        <>
          <div className="relative mb-6 max-w-md">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input
              type="search"
              aria-label={t('launcher.filter')}
              placeholder={t('launcher.filter')}
              className="h-11 pl-9 sm:h-9"
              value={q}
              onChange={(event) => setQ(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && sorted[0]) open(sorted[0][1].apps[0]);
              }}
            />
          </div>
          {shown.length === 0 && <p className="text-sm text-muted">{t('launcher.noMatches', { q: q.trim() })}</p>}
          <div className="grid gap-6">
            {sorted.map(([workspaceId, group]) => (
              <section key={workspaceId} aria-labelledby={`launcher-${workspaceId}`}>
                <h2 id={`launcher-${workspaceId}`} className="mb-2 text-xs font-medium tracking-wide text-muted uppercase">
                  {group.name}
                </h2>
                <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {group.apps.map((app) => (
                    <li key={app.id}>
                      <Link
                        to="/w/$workspaceId/a/$appId"
                        params={{ workspaceId: app.workspace_id, appId: app.id }}
                        className="flex h-full min-h-20 items-start gap-3 rounded-lg border bg-background p-4 transition-colors hover:border-accent hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-accent"
                      >
                        <AppIcon app={app} />
                        <span className="min-w-0 flex-1">
                          <span className="flex items-center gap-2">
                            <span className="truncate font-medium">{app.name}</span>
                            {app.state === 'draft' && <Badge>{t('state.draft')}</Badge>}
                          </span>
                          {app.description && <span className="mt-0.5 line-clamp-2 block text-[13px] text-muted">{app.description}</span>}
                        </span>
                      </Link>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        </>
      )}
    </Page>
  );
}
