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
import { useHasScope } from '@/features/admin/common';

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
  const canCreate = useHasScope('workspace.create');
  const workspaces = data?.filter((workspace) => !workspace.personal_owner_id) ?? [];
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const setCreate = (open: boolean) => void navigate({ search: open ? { create: true } : {}, replace: true });
  const mutation = useMutation({
    mutationFn: () =>
      api<Resource>('/v1/workspaces', { method: 'POST', body: { name: name.trim(), description: description.trim(), keywords: [] } }).then((result) => result.data),
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
          canCreate && (
            <Button variant="primary" onClick={() => setCreate(true)}>
              New workspace
            </Button>
          )
        }
      />
      {isPending ? (
        <Skeleton className="h-24" />
      ) : workspaces.length === 0 ? (
        <Card>
          <EmptyState icon={Briefcase} title="No workspaces yet">
            <p className="mb-3">
              A workspace holds the lists and libraries of a team or a project.
              {!canCreate && ' Ask an administrator to create one or add you to one.'}
            </p>
            {canCreate && (
              <Button variant="primary" onClick={() => setCreate(true)}>
                New workspace
              </Button>
            )}
          </EmptyState>
        </Card>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2">
          {workspaces.map((workspace) => (
            <li key={workspace.id}>
              <Card>
                <Link to="/w/$workspaceId" params={{ workspaceId: workspace.id }} className="flex items-start gap-3 px-4 py-3 hover:bg-surface-muted/60">
                  <Briefcase className="mt-0.5 size-4 shrink-0 text-accent" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{workspace.name}</span>
                    <span className="mt-1 line-clamp-2 text-[13px] text-muted">{workspace.description || 'No description'}</span>
                  </span>
                </Link>
              </Card>
            </li>
          ))}
        </ul>
      )}
      <FormDialog
        open={!!create && canCreate}
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
