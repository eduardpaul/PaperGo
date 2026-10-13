import { useQueries, useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { File, Folder, Library, List, Search as SearchIcon, Briefcase } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApiPages } from '@/api/pages';
import { searchResources, workspacesQuery } from '@/api/queries';
import { searchRequest } from '@/api/requests';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useResourceNames } from '@/features/fields/names';
import { recentWorkspace } from '@/lib/recent-workspace';
import { textParam } from '@/lib/search';
import { useDebounced } from '@/lib/use-debounced';

export const Route = createFileRoute('/_app/search')({
  validateSearch: (search: Record<string, unknown>) => ({
    q: textParam(search.q),
    /** A workspace id, or "all". */
    workspace: textParam(search.workspace),
  }),
  component: SearchPage,
});

/** With every workspace, each shows its first few hits and a link to the rest. */
const perWorkspace = 5;
/** Searching every workspace runs one request each; beyond this many, people pick one. */
const allWorkspacesLimit = 20;

function SearchPage() {
  const { q, workspace } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const workspaces = useQuery(workspacesQuery);
  const list = workspaces.data ?? [];
  const [text, setText] = useState(q ?? '');
  const typed = useDebounced(text.trim(), 300);
  // Searching starts in the workspace given, else the one last opened, else the only one, else all of them.
  const remembered = recentWorkspace();
  const scope =
    workspace ??
    (remembered && list.some((entry) => entry.id === remembered)
      ? remembered
      : list.length === 1
        ? list[0].id
        : 'all');
  useEffect(() => {
    if (typed !== (q ?? '')) void navigate({ search: (current) => ({ ...current, q: typed || undefined }), replace: true });
  }, [typed, q, navigate]);
  const name = (id: string) => {
    const entry = list.find((candidate) => candidate.id === id);
    return entry?.personal_owner_id ? 'Home' : (entry?.name ?? 'Workspace');
  };

  return (
    <Page>
      <PageHeader icon={SearchIcon} title="Search" description="Find items, documents, folders, lists and libraries by name and content." />
      <form
        role="search"
        className="mb-4 flex flex-wrap items-end gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          void navigate({ search: (current) => ({ ...current, q: text.trim() || undefined }) });
        }}
      >
        <div className="min-w-48 flex-1">
          <Label htmlFor="search-q">Search for</Label>
          <Input
            id="search-q"
            className="mt-1"
            autoFocus
            placeholder="Words, or the start of a word…"
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
        </div>
        <div className="w-56">
          <Label htmlFor="search-workspace">In</Label>
          <Select
            id="search-workspace"
            className="mt-1"
            value={scope}
            onChange={(event) => void navigate({ search: (current) => ({ ...current, workspace: event.target.value }) })}
          >
            {list.length > 1 && list.length <= allWorkspacesLimit && <option value="all">All workspaces</option>}
            {list.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {name(entry.id)}
              </option>
            ))}
          </Select>
        </div>
        <Button type="submit" variant="primary">
          Search
        </Button>
      </form>
      {!q ? (
        <EmptyState icon={SearchIcon} title="Type to search">
          Words match anywhere in names and content; the last word may be the start of a word.
        </EmptyState>
      ) : workspaces.isPending ? (
        <Skeleton className="h-16" />
      ) : scope === 'all' && list.length > 1 ? (
        <AllWorkspaces q={q} workspaces={list.slice(0, allWorkspacesLimit)} name={name} />
      ) : (
        <OneWorkspace q={q} workspaceId={scope === 'all' ? (list[0]?.id ?? '') : scope} />
      )}
    </Page>
  );
}

function OneWorkspace({ q, workspaceId }: { q: string; workspaceId: string }) {
  const request = searchRequest(q, workspaceId);
  const results = useApiPages<Resource>(['search', workspaceId, q], request.path, { query: request.query, enabled: !!workspaceId });
  if (results.isPending) return <Skeleton className="h-16" />;
  if (results.isError) return <QueryFeedback query={results} />;
  if (results.items.length === 0) return <EmptyState icon={SearchIcon} title={`Nothing matches “${q}”`} />;
  return (
    <>
      <Results resources={results.items} />
      {results.hasNextPage && (
        <Button className="mt-3" disabled={results.isFetchingNextPage} onClick={() => void results.fetchNextPage()}>
          {results.isFetchingNextPage ? 'Loading…' : 'More results'}
        </Button>
      )}
    </>
  );
}

function AllWorkspaces({ q, workspaces, name }: { q: string; workspaces: Resource[]; name: (id: string) => string }) {
  const results = useQueries({
    queries: workspaces.map((workspace) => ({
      queryKey: ['search', workspace.id, q, perWorkspace],
      queryFn: () => searchResources(q, workspace.id, perWorkspace),
    })),
  });
  if (results.every((result) => result.isPending)) return <Skeleton className="h-16" />;
  const found = workspaces.flatMap((workspace, index) => {
    const page = results[index].data;
    return page?.value?.length ? [{ workspace, page }] : [];
  });
  if (found.length === 0 && results.every((result) => !result.isPending))
    return <EmptyState icon={SearchIcon} title={`Nothing matches “${q}” in any workspace`} />;
  return (
    <div className="flex flex-col gap-5">
      {found.map(({ workspace, page }) => (
        <section key={workspace.id} aria-label={name(workspace.id)}>
          <h2 className="mb-2 flex items-center gap-2 text-sm font-semibold">
            <Briefcase className="size-4 text-muted" /> {name(workspace.id)}
          </h2>
          <Results resources={page.value ?? []} />
          {page['@odata.nextLink'] && (
            <Link
              to="/search"
              search={{ q, workspace: workspace.id }}
              className="mt-2 inline-block text-[13px] text-accent hover:underline"
            >
              All results in {name(workspace.id)}
            </Link>
          )}
        </section>
      ))}
    </div>
  );
}

const kinds: Record<string, { label: string; icon: typeof File }> = {
  item: { label: 'Item', icon: File },
  folder: { label: 'Folder', icon: Folder },
  list: { label: 'List', icon: List },
  library: { label: 'Library', icon: Library },
  workspace: { label: 'Workspace', icon: Briefcase },
};

/** Hits with what they are and where: "Item in Invoices". */
function Results({ resources }: { resources: Resource[] }) {
  const containers = useResourceNames(
    [...new Set(resources.flatMap((resource) => (resource.container_id ? [resource.container_id] : [])))].sort(),
  );
  return (
    <ul className="overflow-hidden rounded-lg border bg-surface">
      {resources.map((resource) => (
        <li key={resource.id} className="border-b last:border-0">
          <Result resource={resource} where={resource.container_id ? containers.get(resource.container_id) : undefined} />
        </li>
      ))}
    </ul>
  );
}

function Result({ resource, where }: { resource: Resource; where?: string }) {
  const navigate = useNavigate();
  const kind = kinds[resource.kind] ?? kinds.item;
  const Icon = kind.icon;
  const open = () => {
    if ((resource.kind === 'item' || resource.kind === 'folder') && resource.container_id) {
      void navigate({
        to: '/w/$workspaceId/l/$listId',
        params: { workspaceId: resource.workspace_id, listId: resource.container_id },
        search: resource.kind === 'folder' ? { folder: resource.id } : { item: resource.id },
      });
      return;
    }
    if (resource.kind === 'list' || resource.kind === 'library') {
      void navigate({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: resource.workspace_id, listId: resource.id } });
      return;
    }
    void navigate({ to: '/w/$workspaceId', params: { workspaceId: resource.id } });
  };
  return (
    <button type="button" className="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-surface-muted" onClick={open}>
      <Icon className="size-4 shrink-0 text-muted" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{resource.name}</span>
        <span className="text-xs text-muted">
          {kind.label}
          {where ? ` in ${where}` : ''}
        </span>
      </span>
    </button>
  );
}
