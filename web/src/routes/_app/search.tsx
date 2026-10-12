import { useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { Search as SearchIcon } from 'lucide-react';
import { useState } from 'react';
import { searchResources, workspacesQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { textParam } from '@/lib/search';

export const Route = createFileRoute('/_app/search')({
  validateSearch: (search: Record<string, unknown>) => ({
    q: textParam(search.q),
    workspace: textParam(search.workspace),
  }),
  component: SearchPage,
});

function SearchPage() {
  const { q, workspace } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const workspaces = useQuery(workspacesQuery);
  const [text, setText] = useState(q ?? '');
  const [workspaceId, setWorkspaceId] = useState(workspace ?? '');
  const results = useQuery({
    queryKey: ['search', workspace, q],
    enabled: !!q && !!workspace,
    queryFn: () => searchResources(q!, workspace!),
  });

  return (
    <Page>
      <PageHeader icon={SearchIcon} title="Search" description="Find items, folders and collections in one workspace." />
      <form
        className="mb-4 flex flex-wrap items-end gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          void navigate({ search: { q: text.trim() || undefined, workspace: workspaceId || undefined } });
        }}
      >
        <div className="min-w-48 flex-1">
          <Label htmlFor="search-q">Query</Label>
          <Input id="search-q" className="mt-1" value={text} onChange={(event) => setText(event.target.value)} />
        </div>
        <div className="w-56">
          <Label htmlFor="search-workspace">Workspace</Label>
          <Select id="search-workspace" className="mt-1" value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)}>
            <option value="">Select…</option>
            {workspaces.data?.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.personal_owner_id ? 'Home' : entry.name}
              </option>
            ))}
          </Select>
        </div>
        <Button type="submit" variant="primary">
          Search
        </Button>
      </form>
      {!workspace || !q ? (
        <EmptyState icon={SearchIcon} title="Choose a workspace and a query" />
      ) : (
        <ul className="overflow-hidden rounded-lg border bg-surface">
          {(results.data?.value ?? []).map((resource) => (
            <li key={resource.id} className="border-b last:border-0">
              <Result resource={resource} />
            </li>
          ))}
          {results.isFetched && (results.data?.value?.length ?? 0) === 0 && <EmptyState title="No results" />}
        </ul>
      )}
    </Page>
  );
}

function Result({ resource }: { resource: Resource }) {
  const navigate = useNavigate();
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
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{resource.name}</span>
        <span className="text-xs text-muted">{resource.kind}</span>
      </span>
    </button>
  );
}
