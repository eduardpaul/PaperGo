import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { Briefcase, File, Folder, Search as SearchIcon, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api } from '@/api/http';
import { SearchRelationships } from '@/features/search/search-relationships';
import { useApiPages } from '@/api/pages';
import { workflowCatalogQuery, workspacesQuery } from '@/api/queries';
import { searchRequest } from '@/api/requests';
import { cn } from '@/lib/utils';
import type { SearchContentType, SearchFacetValue, SearchHit, SearchPage } from '@/api/types';
import { treeLabel, typeTree } from '@/features/lists/schema';
import { FileAction } from '@/features/files/file-action';
import { FileTypeIcon } from '@/features/files/file-types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useResourceNames, useTermNames } from '@/features/fields/names';
import { useTranslation } from '@/i18n';
import { highlightParts } from '@/lib/highlight';
import { useFormat } from '@/lib/preferences';
import { textParam } from '@/lib/search';
import { useDebounced } from '@/lib/use-debounced';

interface SearchRouteParams { archive?: 'active' | 'archived' | 'all'; q?: string; workspace?: string; collection?: string; keyword?: string; content_type?: string; kind?: Kind }

type Kind = 'item' | 'folder' | 'all';

export const Route = createFileRoute('/_app/search')({
  validateSearch: (search: Record<string, unknown>): SearchRouteParams => ({
    archive: (['active', 'archived', 'all'] as const).find(value => value === search.archive),
    content_type: textParam(search.content_type),
    q: textParam(search.q),
    workspace: textParam(search.workspace),
    collection: textParam(search.collection),
    keyword: textParam(search.keyword),
    kind: (['item', 'folder', 'all'] as const).find((kind) => kind === search.kind),
  }),
  component: SearchRoute,
});

// SearchRoute searches when the deployment runs search, which is optional.
function SearchRoute() {
  const { t } = useTranslation('search');
  const catalog = useQuery(workflowCatalogQuery);
  if (catalog.isPending) return <Page><Skeleton className="h-40" /></Page>;
  if (catalog.data?.roles?.some((role) => role.key === 'search.index')) return <SearchPage />;
  return (
    <Page>
      <PageHeader icon={SearchIcon} title={t('title')} />
      <EmptyState icon={SearchIcon} title={t('notSetUp')}>
        {t('notSetUpBody')}
      </EmptyState>
    </Page>
  );
}

function SearchPage() {
  const { t } = useTranslation('search');
  const params = Route.useSearch();
  const { q, workspace, collection, keyword, content_type } = params;
  const archive = params.archive ?? 'active';
  const types = useQuery({ queryKey: ['search', 'content-types', workspace, collection], queryFn: async () => (await api<SearchContentType[]>('/v1/search/content-types', { query: { workspace_id: workspace, collection_id: collection } })).data });
  const kind: Kind = params.kind ?? 'item';
  const navigate = useNavigate({ from: Route.fullPath });
  const workspaces = useQuery(workspacesQuery);
  const [text, setText] = useState(q ?? '');
  const typed = useDebounced(text.trim(), 300);
  useEffect(() => {
    if (typed !== (q ?? ''))
      void navigate({
        search: (current) => ({ ...current, q: typed || undefined }),
        replace: true,
      });
  }, [typed, q, navigate]);
  const workspaceName = (id: string) => {
    const entry = workspaces.data?.find((candidate) => candidate.id === id);
    return entry?.personal_owner_id ? t('home') : (entry?.name ?? t('workspace'));
  };
  const filtered = archive === 'archived' || !!(workspace || collection || keyword || content_type);
  // Workspaces are not in the search index: their names match here.
  const places = q
    ? (workspaces.data ?? []).filter((entry) => workspaceName(entry.id).toLowerCase().includes(q.toLowerCase())).slice(0, 8)
    : [];

  return (
    <Page>
      <PageHeader icon={SearchIcon} title={t('title')} description={t('description')} />
      <form
        role="search"
        className="mb-4 flex flex-wrap items-end gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          void navigate({
            search: (current) => ({ ...current, q: text.trim() || undefined }),
          });
        }}
      >
        <div className="min-w-48 flex-1">
          <Label htmlFor="search-q">{t('searchFor')}</Label>
          <Input
            id="search-q"
            className="mt-1"
            autoFocus
            placeholder={t('placeholder')}
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
        </div>
        <div className="w-40"><Label htmlFor="search-archive">{t('items')}</Label><Select id="search-archive" value={archive} onChange={event => void navigate({ search: current => ({ ...current, archive: event.target.value as 'active' | 'archived' | 'all' }) })}><option value="active">{t('archive.active')}</option><option value="archived">{t('archive.archived')}</option><option value="all">{t('archive.all')}</option></Select></div>
        <div className="w-48">
          <Label htmlFor="search-kind">{t('find')}</Label>
          <Select
            id="search-kind"
            className="mt-1"
            value={kind}
            onChange={(event) =>
              void navigate({
                search: (current) => ({
                  ...current,
                  kind: event.target.value === 'item' ? undefined : (event.target.value as Kind),
                }),
              })
            }
          >
            <option value="item">{t('kind.item')}</option>
            <option value="folder">{t('kind.folder')}</option>
            <option value="all">{t('kind.all')}</option>
          </Select>
        </div>
        <div className="w-52"><Label htmlFor="search-type">{t('contentType')}</Label>
          <Select id="search-type" className="mt-1" value={content_type ?? ''} onChange={event => void navigate({ search: current => ({ ...current, content_type: event.target.value || undefined }) })}>
            <option value="">{t('allContentTypes')}</option>
            {typeTree(types.data ?? []).map(({ type, depth }) => <option key={type.id} value={type.id}>{treeLabel(type.name, depth)}</option>)}
            {content_type && !types.data?.some(type => type.id === content_type) && <option value={content_type}>{t('selectedContentType')}</option>}
          </Select>
        </div>
        <Button type="submit" variant="primary">
          {t('submit')}
        </Button>
      </form>
      {places.length > 0 && (
        <nav aria-label={t('workspaces')} className="mb-4 flex flex-wrap gap-2">
          {places.map((entry) => (
            <Link
              key={entry.id}
              to="/w/$workspaceId"
              params={{ workspaceId: entry.id }}
              className="flex items-center gap-1.5 rounded-md border bg-surface px-2.5 py-1 text-[13px] hover:bg-surface-muted"
            >
              <Briefcase className="size-3.5 text-muted" /> {workspaceName(entry.id)}
            </Link>
          ))}
        </nav>
      )}
      {!q && !filtered ? (
        <EmptyState icon={SearchIcon} title={t('typeToSearch')}>
          {t('typeToSearchBody')}
        </EmptyState>
      ) : (
        <Results archive={archive} contentType={content_type} typeNames={new Map((types.data ?? []).map(type => [type.id, type.name]))} q={q} workspace={workspace} collection={collection} keyword={keyword} kind={kind} workspaceName={workspaceName} />
      )}
    </Page>
  );
}

function Results({
  archive,
  contentType,
  typeNames,
  q,
  workspace,
  collection,
  keyword,
  kind,
  workspaceName,
}: {
  archive: 'active' | 'archived' | 'all';
  contentType?: string;
  typeNames: Map<string, string>;
  q?: string;
  workspace?: string;
  collection?: string;
  keyword?: string;
  kind: Kind;
  workspaceName: (id: string) => string;
}) {
  const { t } = useTranslation(['search', 'common']);
  const navigate = useNavigate({ from: Route.fullPath });
  const request = searchRequest({
    archive,
    q,
    workspaceId: workspace,
    collectionId: collection,
    contentTypeId: contentType,
    keyword,
    kind,
  });
  const results = useApiPages<SearchHit>(['search', archive, q, workspace, collection, keyword, kind, contentType], request.path, { query: request.query });
  const first = results.data?.pages[0] as SearchPage | undefined;
  const facets = first?.facets;
  const keywordNames = useTermNames([...new Set([...(facets?.keyword ?? []).map(entry => entry.value), ...(keyword ? [keyword] : [])])]);
  const collectionIds = [
    ...new Set([
      ...results.items.map((hit) => hit.collection_id),
      ...(facets?.collection ?? []).map((value) => value.value),
      ...(collection ? [collection] : []),
    ]),
  ].sort();
  const collectionNames = useResourceNames(collectionIds);
  const collectionName = (id: string) => collectionNames.get(id) ?? t('list');
  const filter = (patch: { workspace?: string; collection?: string; keyword?: string; content_type?: string }) =>
    void navigate({ search: (current) => ({ ...current, ...patch }) });

  if (results.isPending) return <Skeleton className="h-16" />;
  if (results.isError) return <QueryFeedback query={results} />;
  const chips = [
    contentType && { label: typeNames.get(contentType) ?? t('contentType'), clear: { content_type: undefined } },
    workspace && {
      label: workspaceName(workspace),
      clear: { workspace: undefined, collection: undefined },
    },
    collection && {
      label: collectionName(collection),
      clear: { collection: undefined },
    },
    keyword && { label: keywordNames.get(keyword) ?? t('keyword'), clear: { keyword: undefined } },
  ].filter((chip) => !!chip);
  const total = first?.total ?? results.items.length;

  return (
    <div className="grid gap-5 md:grid-cols-[1fr_14rem]">
      <section aria-label={t('results')} className="min-w-0">
        <div className="mb-2 flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <span>{first?.total_estimated ? t('aboutResults', { count: total }) : t('resultCount', { count: total })}</span>
          {chips.map((chip) => (
            <button
              key={chip.label}
              type="button"
              className="flex items-center gap-1 rounded-full border bg-surface px-2 py-0.5 text-xs text-fg hover:bg-surface-muted"
              aria-label={t('removeFilter', { label: chip.label })}
              onClick={() => filter(chip.clear)}
            >
              {chip.label} <X className="size-3" />
            </button>
          ))}
        </div>
        {results.items.length === 0 ? (
          <EmptyState icon={SearchIcon} title={q ? t('nothingMatches', { q }) : t('nothingMatchesFilters')} />
        ) : (
          <ul className="overflow-hidden rounded-lg border bg-surface">
            {results.items.map((hit) => (
              <li key={`${hit.id}:${hit.surface}`} className="border-b last:border-0">
                <Hit hit={hit} query={q} where={`${collectionName(hit.collection_id)} · ${workspaceName(hit.workspace_id)}`} />
              </li>
            ))}
          </ul>
        )}
        {results.hasNextPage && (
          <Button className="mt-3" disabled={results.isFetchingNextPage} onClick={() => void results.fetchNextPage()}>
            {results.isFetchingNextPage ? t('common:status.loading') : t('moreResults')}
          </Button>
        )}
      </section>
      {facets && (
        <aside aria-label={t('refine')} className="flex flex-col gap-4">
          {!workspace && (
            <Facet title={t('workspaces')} values={facets.workspace} label={workspaceName} onPick={(value) => filter({ workspace: value })} />
          )}
          {!collection && (
            <Facet
              title={t('listsAndLibraries')}
              values={facets.collection}
              label={collectionName}
              onPick={(value) => filter({ collection: value })}
            />
          )}
          {!contentType && <Facet title={t('contentTypes')} values={facets.content_type ?? []} label={id => typeNames.get(id) ?? t('contentType')} onPick={value => filter({ content_type: value })} />}
          {!keyword && <Facet title={t('keywords')} values={facets.keyword} label={(value) => keywordNames.get(value) ?? value} onPick={(value) => filter({ keyword: value })} />}
        </aside>
      )}
    </div>
  );
}

function Facet({
  title,
  values,
  label,
  onPick,
}: {
  title: string;
  values: SearchFacetValue[];
  label: (value: string) => string;
  onPick: (value: string) => void;
}) {
  if (values.length === 0) return null;
  return (
    <section aria-label={title}>
      <h2 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{title}</h2>
      <ul className="flex flex-col">
        {values.slice(0, 10).map((value) => (
          <li key={value.value}>
            <button
              type="button"
              className="flex w-full items-center justify-between gap-2 rounded px-1.5 py-1 text-left text-[13px] hover:bg-surface-muted"
              onClick={() => onPick(value.value)}
            >
              <span className="truncate">{label(value.value)}</span>
              <span className="text-xs text-muted">{value.count}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** A hit with where it is, when it changed, and the matching text. */
export function Hit({ hit, where, query }: { hit: SearchHit; where: string; query?: string }) {
  const { t } = useTranslation('search');
  const navigate = useNavigate();
  const format = useFormat();
  const Icon = hit.kind === 'folder' ? Folder : File;
  const open = () =>
    void navigate({
      to: '/w/$workspaceId/l/$listId',
      params: { workspaceId: hit.workspace_id, listId: hit.collection_id },
      search: hit.kind === 'folder' ? { folder: hit.id } : { item: hit.id, ...(hit.archived ? { view: 'archived' } : {}) },
    });
  return (
    <div className="relative"><button type="button" className={cn('flex w-full items-start gap-3 px-4 py-2.5 text-left hover:bg-surface-muted', hit.file && 'pr-14')} onClick={open}>
      {hit.file ? <FileTypeIcon media={hit.file.content_type} className="mt-0.5" /> : <Icon className="mt-0.5 size-4 shrink-0 text-muted" />}
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{hit.name}</span>
          {hit.archived && <span className="rounded bg-surface-muted px-1.5 text-[11px] text-muted">{t('hit.archived')}</span>}
          {hit.surface === 'head' && <span className="rounded bg-surface-muted px-1.5 text-[11px] text-muted">{t('hit.draft')}</span>}
        </span>
        <span className="block text-xs text-muted">
          {[
            t(hit.kind === 'folder' ? 'hit.folderIn' : 'hit.itemIn', { where }),
            format.dateTime(hit.modified_at),
            hit.page && t('hit.page', { page: hit.page }),
            hit.matched_by.length === 1 && hit.matched_by[0] === 'semantic' && t('hit.similar'),
          ]
            .filter(Boolean)
            .join(' · ')}
        </span>
        {hit.snippet && (
          <span className="mt-1 block text-[13px] text-fg/80" data-testid="snippet">
            {highlightParts(hit.snippet, hit.highlights).map((part, index) =>
              part.mark ? (
                <mark key={index} className="rounded-sm bg-accent/20 text-fg">
                  {part.text}
                </mark>
              ) : (
                <span key={index}>{part.text}</span>
              ),
            )}
          </span>
        )}
      </span>
    </button>
    {hit.file && (
      <FileAction
        item={{ id: hit.id, workspace_id: hit.workspace_id, container_id: hit.collection_id, name: hit.name, file: hit.file }}
        // The file opens at the matching page with the searched words found in it.
        options={{ page: hit.page, find: query?.replace(/["*]/g, '').trim() || undefined }}
        className="absolute top-2 right-3"
      />
    )}
    {hit.kind === 'item' && <SearchRelationships itemId={hit.id} />}
    </div>
  );
}
