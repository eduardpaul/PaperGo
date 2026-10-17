import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { Briefcase, File, Folder, Search as SearchIcon, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api } from '@/api/http';
import { SearchRelationships } from '@/features/search/search-relationships';
import { useApiPages } from '@/api/pages';
import { workspacesQuery } from '@/api/queries';
import { searchRequest } from '@/api/requests';
import type { SearchFacetValue, SearchHit, SearchPage } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useResourceNames, useTermNames } from '@/features/fields/names';
import { highlightParts } from '@/lib/highlight';
import { useFormat } from '@/lib/preferences';
import { textParam } from '@/lib/search';
import { useDebounced } from '@/lib/use-debounced';

interface SearchRouteParams { q?: string; workspace?: string; collection?: string; keyword?: string; content_type?: string; kind?: Kind }

type Kind = 'item' | 'folder' | 'all';

export const Route = createFileRoute('/_app/search')({
  validateSearch: (search: Record<string, unknown>): SearchRouteParams => ({
    content_type: textParam(search.content_type),
    q: textParam(search.q),
    workspace: textParam(search.workspace),
    collection: textParam(search.collection),
    keyword: textParam(search.keyword),
    kind: (['item', 'folder', 'all'] as const).find((kind) => kind === search.kind),
  }),
  component: SearchPage,
});

function SearchPage() {
  const params = Route.useSearch();
  const { q, workspace, collection, keyword, content_type } = params;
  const types = useQuery({ queryKey: ['search', 'content-types', workspace, collection], queryFn: async () => (await api<{ id: string; name: string }[]>('/v1/search/content-types', { query: { workspace_id: workspace, collection_id: collection } })).data });
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
    return entry?.personal_owner_id ? 'Home' : (entry?.name ?? 'Workspace');
  };
  const filtered = !!(workspace || collection || keyword || content_type);
  // Workspaces are not in the search index: their names match here.
  const places = q
    ? (workspaces.data ?? []).filter((entry) => workspaceName(entry.id).toLowerCase().includes(q.toLowerCase())).slice(0, 8)
    : [];

  return (
    <Page>
      <PageHeader icon={SearchIcon} title="Search" description="Find items and folders across your workspaces by name, fields and keywords." />
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
          <Label htmlFor="search-q">Search for</Label>
          <Input
            id="search-q"
            className="mt-1"
            autoFocus
            placeholder={'Words, "a phrase", word*, -exclude'}
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
        </div>
        <div className="w-48">
          <Label htmlFor="search-kind">Find</Label>
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
            <option value="item">Items</option>
            <option value="folder">Folders</option>
            <option value="all">Items and folders</option>
          </Select>
        </div>
        <div className="w-52"><Label htmlFor="search-type">Content type</Label>
          <Select id="search-type" className="mt-1" value={content_type ?? ''} onChange={event => void navigate({ search: current => ({ ...current, content_type: event.target.value || undefined }) })}>
            <option value="">All content types</option>
            {types.data?.map(type => <option key={type.id} value={type.id}>{type.name}</option>)}
            {content_type && !types.data?.some(type => type.id === content_type) && <option value={content_type}>Selected content type</option>}
          </Select>
        </div>
        <Button type="submit" variant="primary">
          Search
        </Button>
      </form>
      {places.length > 0 && (
        <nav aria-label="Workspaces" className="mb-4 flex flex-wrap gap-2">
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
        <EmptyState icon={SearchIcon} title="Type to search">
          Every word must match, in names, fields or keywords; other forms of a word match too. Use quotes for a phrase, * for the start of a
          word and - to leave a word out.
        </EmptyState>
      ) : (
        <Results contentType={content_type} typeNames={new Map((types.data ?? []).map(type => [type.id, type.name]))} q={q} workspace={workspace} collection={collection} keyword={keyword} kind={kind} workspaceName={workspaceName} />
      )}
    </Page>
  );
}

function Results({
  contentType,
  typeNames,
  q,
  workspace,
  collection,
  keyword,
  kind,
  workspaceName,
}: {
  contentType?: string;
  typeNames: Map<string, string>;
  q?: string;
  workspace?: string;
  collection?: string;
  keyword?: string;
  kind: Kind;
  workspaceName: (id: string) => string;
}) {
  const navigate = useNavigate({ from: Route.fullPath });
  const request = searchRequest({
    q,
    workspaceId: workspace,
    collectionId: collection,
    contentTypeId: contentType,
    keyword,
    kind,
  });
  const results = useApiPages<SearchHit>(['search', q, workspace, collection, keyword, kind, contentType], request.path, { query: request.query });
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
  const collectionName = (id: string) => collectionNames.get(id) ?? 'List';
  const filter = (patch: { workspace?: string; collection?: string; keyword?: string; content_type?: string }) =>
    void navigate({ search: (current) => ({ ...current, ...patch }) });

  if (results.isPending) return <Skeleton className="h-16" />;
  if (results.isError) return <QueryFeedback query={results} />;
  const chips = [
    contentType && { label: typeNames.get(contentType) ?? 'Content type', clear: { content_type: undefined } },
    workspace && {
      label: workspaceName(workspace),
      clear: { workspace: undefined, collection: undefined },
    },
    collection && {
      label: collectionName(collection),
      clear: { collection: undefined },
    },
    keyword && { label: keywordNames.get(keyword) ?? 'Keyword', clear: { keyword: undefined } },
  ].filter((chip) => !!chip);
  const total = first?.total ?? results.items.length;

  return (
    <div className="grid gap-5 md:grid-cols-[1fr_14rem]">
      <section aria-label="Results" className="min-w-0">
        <div className="mb-2 flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <span>
            {first?.total_estimated ? 'About ' : ''}
            {total} {total === 1 ? 'result' : 'results'}
          </span>
          {chips.map((chip) => (
            <button
              key={chip.label}
              type="button"
              className="flex items-center gap-1 rounded-full border bg-surface px-2 py-0.5 text-xs text-fg hover:bg-surface-muted"
              aria-label={`Remove filter ${chip.label}`}
              onClick={() => filter(chip.clear)}
            >
              {chip.label} <X className="size-3" />
            </button>
          ))}
        </div>
        {results.items.length === 0 ? (
          <EmptyState icon={SearchIcon} title={q ? `Nothing matches “${q}”` : 'Nothing matches these filters'} />
        ) : (
          <ul className="overflow-hidden rounded-lg border bg-surface">
            {results.items.map((hit) => (
              <li key={`${hit.id}:${hit.surface}`} className="border-b last:border-0">
                <Hit hit={hit} where={`${collectionName(hit.collection_id)} · ${workspaceName(hit.workspace_id)}`} />
              </li>
            ))}
          </ul>
        )}
        {results.hasNextPage && (
          <Button className="mt-3" disabled={results.isFetchingNextPage} onClick={() => void results.fetchNextPage()}>
            {results.isFetchingNextPage ? 'Loading…' : 'More results'}
          </Button>
        )}
      </section>
      {facets && (
        <aside aria-label="Refine" className="flex flex-col gap-4">
          {!workspace && (
            <Facet title="Workspaces" values={facets.workspace} label={workspaceName} onPick={(value) => filter({ workspace: value })} />
          )}
          {!collection && (
            <Facet
              title="Lists and libraries"
              values={facets.collection}
              label={collectionName}
              onPick={(value) => filter({ collection: value })}
            />
          )}
          {!contentType && <Facet title="Content types" values={facets.content_type ?? []} label={id => typeNames.get(id) ?? 'Content type'} onPick={value => filter({ content_type: value })} />}
          {!keyword && <Facet title="Keywords" values={facets.keyword} label={(value) => keywordNames.get(value) ?? value} onPick={(value) => filter({ keyword: value })} />}
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
export function Hit({ hit, where }: { hit: SearchHit; where: string }) {
  const navigate = useNavigate();
  const format = useFormat();
  const Icon = hit.kind === 'folder' ? Folder : File;
  const open = () =>
    void navigate({
      to: '/w/$workspaceId/l/$listId',
      params: { workspaceId: hit.workspace_id, listId: hit.collection_id },
      search: hit.kind === 'folder' ? { folder: hit.id } : { item: hit.id },
    });
  return (
    <div><button type="button" className="flex w-full items-start gap-3 px-4 py-2.5 text-left hover:bg-surface-muted" onClick={open}>
      <Icon className="mt-0.5 size-4 shrink-0 text-muted" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{hit.name}</span>
          {hit.surface === 'head' && <span className="rounded bg-surface-muted px-1.5 text-[11px] text-muted">Draft</span>}
        </span>
        <span className="block text-xs text-muted">
          {hit.kind === 'folder' ? 'Folder' : 'Item'} in {where} · {format.dateTime(hit.modified_at)}
          {hit.page ? ` · page ${hit.page}` : ''}
          {hit.matched_by.length === 1 && hit.matched_by[0] === 'semantic' ? ' · similar meaning' : ''}
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
    </button>{hit.kind === 'item' && <SearchRelationships itemId={hit.id} />}
    </div>
  );
}
