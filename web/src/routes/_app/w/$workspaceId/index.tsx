import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { Settings } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { collectionChildren, keys, resourceQuery, templatesQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';

export const Route = createFileRoute('/_app/w/$workspaceId/')({
  component: WorkspacePage,
});

function WorkspacePage() {
  const { workspaceId } = Route.useParams();
  const queryClient = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const collections = useQuery({
    queryKey: ['resources', workspaceId, 'collections'],
    queryFn: () => collectionChildren(workspaceId),
  });
  const templates = useQuery(templatesQuery);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [template, setTemplate] = useState('');
  const [kind, setKind] = useState<'list' | 'library'>('list');
  const create = useMutation({
    mutationFn: () =>
      api<Resource>(`/v1/resources/${workspaceId}/children`, {
        method: 'POST',
        body: template
          ? { name: name.trim(), description: description.trim(), template_key: template, tags: [], values: {} }
          : { kind, name: name.trim(), description: description.trim(), tags: [], values: {} },
      }).then((result) => result.data),
    onSuccess: async () => {
      setOpen(false);
      setName('');
      setDescription('');
      await queryClient.invalidateQueries({ queryKey: ['resources', workspaceId, 'collections'] });
      await queryClient.invalidateQueries({ queryKey: keys.resources });
    },
  });

  return (
    <Page>
      <PageHeader
        title={workspace.data?.name ?? 'Workspace'}
        description={workspace.data?.description}
        actions={
          <>
            <Button asChild>
              <Link to="/w/$workspaceId/settings" params={{ workspaceId }}>
                <Settings /> Settings
              </Link>
            </Button>
            <Button asChild>
              <Link to="/w/$workspaceId/tags" params={{ workspaceId }}>
                Tags
              </Link>
            </Button>
            <Button variant="primary" onClick={() => setOpen(true)}>
              New collection
            </Button>
          </>
        }
      />
      {collections.isPending ? (
        <Skeleton className="h-24" />
      ) : (collections.data?.length ?? 0) === 0 ? (
        <EmptyState title="No lists or libraries">Create one to start adding items.</EmptyState>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2">
          {collections.data?.map((collection) => (
            <li key={collection.id}>
              <Card>
                <Link
                  to="/w/$workspaceId/l/$listId"
                  params={{ workspaceId, listId: collection.id }}
                  className="block px-4 py-3 hover:bg-surface-muted/60"
                >
                  <span className="block truncate font-medium">{collection.name}</span>
                  <span className="text-xs text-muted">{collection.kind === 'library' ? 'Library' : 'List'}</span>
                </Link>
              </Card>
            </li>
          ))}
        </ul>
      )}
      <FormDialog
        open={open}
        title="New collection"
        submitLabel="Create"
        pending={create.isPending}
        onClose={() => setOpen(false)}
        onSubmit={() => name.trim() && create.mutate()}
      >
        <Label htmlFor="collection-name">Name</Label>
        <Input id="collection-name" value={name} onChange={(event) => setName(event.target.value)} required autoFocus />
        <Label htmlFor="collection-description">Description</Label>
        <Textarea
          id="collection-description"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
        <Label htmlFor="collection-template">Template</Label>
        <Select id="collection-template" value={template} onChange={(event) => setTemplate(event.target.value)}>
          <option value="">None</option>
          {templates.data?.map((entry) => (
            <option key={entry.key} value={entry.key}>
              {entry.name}
            </option>
          ))}
        </Select>
        {!template && (
          <>
            <Label htmlFor="collection-kind">Kind</Label>
            <Select
              id="collection-kind"
              value={kind}
              onChange={(event) => setKind(event.target.value as 'list' | 'library')}
            >
              <option value="list">List</option>
              <option value="library">Library</option>
            </Select>
          </>
        )}
      </FormDialog>
    </Page>
  );
}
