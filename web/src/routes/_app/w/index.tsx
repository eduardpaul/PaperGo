import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { Briefcase } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys, workspacesQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label, Textarea } from '@/components/ui/input';

export const Route = createFileRoute('/_app/w/')({
  validateSearch: (search: Record<string, unknown>): { create?: boolean } => ({
    create: search.create === true || search.create === 'true' ? true : undefined,
  }),
  component: WorkspacesPage,
});

function WorkspacesPage() {
  const { create } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const queryClient = useQueryClient();
  const { data, isPending } = useQuery(workspacesQuery);
  const workspaces = data?.filter((workspace) => !workspace.personal_owner_id) ?? [];
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const setCreate = (open: boolean) => void navigate({ search: open ? { create: true } : {}, replace: true });
  const mutation = useMutation({
    mutationFn: () =>
      api<Resource>('/v1/workspaces', { method: 'POST', body: { name: name.trim(), description: description.trim(), tags: [] } }).then((result) => result.data),
    onSuccess: async (workspace) => {
      await queryClient.invalidateQueries({ queryKey: keys.workspaces });
      void navigate({ to: '/w/$workspaceId', params: { workspaceId: workspace.id } });
    },
  });

  return (
    <Page>
      <PageHeader
        icon={Briefcase}
        title="Workspaces"
        description="Shared places for a team or a project."
        actions={
          <Button variant="primary" onClick={() => setCreate(true)}>
            New workspace
          </Button>
        }
      />
      {isPending ? (
        <Skeleton className="h-24" />
      ) : workspaces.length === 0 ? (
        <EmptyState icon={Briefcase} title="No workspaces yet">
          <button type="button" className="font-medium text-accent hover:underline" onClick={() => setCreate(true)}>
            Create a workspace
          </button>
        </EmptyState>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2">
          {workspaces.map((workspace) => (
            <li key={workspace.id}>
              <Card>
                <Link to="/w/$workspaceId" params={{ workspaceId: workspace.id }} className="block px-4 py-3 hover:bg-surface-muted/60">
                  <span className="block truncate font-medium">{workspace.name}</span>
                  {workspace.description && <span className="mt-1 block truncate text-[13px] text-muted">{workspace.description}</span>}
                </Link>
              </Card>
            </li>
          ))}
        </ul>
      )}
      <FormDialog
        open={!!create}
        title="New workspace"
        submitLabel="Create"
        pending={mutation.isPending}
        onClose={() => setCreate(false)}
        onSubmit={() => name.trim() && mutation.mutate()}
      >
        <Label htmlFor="workspace-name">Name</Label>
        <Input id="workspace-name" value={name} onChange={(event) => setName(event.target.value)} required autoFocus />
        <Label htmlFor="workspace-description">Description</Label>
        <Textarea id="workspace-description" value={description} onChange={(event) => setDescription(event.target.value)} />
      </FormDialog>
    </Page>
  );
}
