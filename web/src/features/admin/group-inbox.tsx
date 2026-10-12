import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api, isApiError } from '@/api/http';
import { allPages } from '@/api/pages';
import { keys } from '@/api/queries';
import type { Inbox, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback, useHasScope } from './common';

/** Shared inbox selector ported from PaperDotNet group details. */
export function GroupInbox({ groupId }: { groupId: string }) {
  const canRead = useHasScope('document.read');
  const canEdit = useHasScope('document.write');
  const client = useQueryClient();
  const inbox = useQuery({
    queryKey: ['groups', groupId, 'inbox'],
    enabled: canRead,
    queryFn: async () => {
      try {
        return (await api<Inbox>(`/v1/identity/groups/${groupId}/inbox`)).data;
      } catch (error) {
        if (isApiError(error, 404)) return null;
        throw error;
      }
    },
  });
  const libraries = useQuery({
    queryKey: ['resources', 'inbox-libraries'],
    enabled: canRead,
    queryFn: async () => {
      const workspaces = await allPages<Resource>('/v1/workspaces');
      return (
        await Promise.all(
          workspaces.map(async (workspace) =>
            (await allPages<Resource>(`/v1/resources/${workspace.id}/children`))
              .filter((resource) => resource.kind === 'library')
              .map((resource) => ({ ...resource, workspaceName: workspace.name })),
          ),
        )
      ).flat();
    },
  });
  if (!canRead) return null;
  return (
    <div className="my-4 grid gap-2 border-t pt-3">
      <Label htmlFor={`group-inbox-${groupId}`}>Shared inbox</Label>
      <p className="text-xs text-muted">
        Changing the inbox requires manage access to both the current and destination libraries.
      </p>
      <QueryFeedback query={inbox} />
      <QueryFeedback query={libraries} />
      {inbox.isSuccess && (
        <InboxSelector
          key={inbox.data?.library_id ?? 'empty'}
          groupId={groupId}
          current={inbox.data}
          libraries={libraries.data ?? []}
          canEdit={canEdit}
          onSaved={async () => {
            await client.invalidateQueries({ queryKey: ['groups', groupId, 'inbox'] });
            await client.invalidateQueries({ queryKey: keys.inboxes });
          }}
        />
      )}
    </div>
  );
}

function InboxSelector({
  groupId,
  current,
  libraries,
  canEdit,
  onSaved,
}: {
  groupId: string;
  current: Inbox | null;
  libraries: (Resource & { workspaceName: string })[];
  canEdit: boolean;
  onSaved: () => Promise<void>;
}) {
  const [id, setId] = useState(current?.library_id ?? '');
  const save = useMutation({
    mutationFn: () =>
      api(
        `/v1/identity/groups/${groupId}/inbox`,
        id ? { method: 'PUT', body: { library_id: id } } : { method: 'DELETE' },
      ),
    onSuccess: onSaved,
  });
  return (
    <>
      <Select
        id={`group-inbox-${groupId}`}
        value={id}
        disabled={!canEdit || save.isPending}
        onChange={(event) => setId(event.target.value)}
      >
        <option value="">No readable inbox</option>
        {current && !libraries.some((library) => library.id === current.library_id) && (
          <option value={current.library_id}>{current.library_name}</option>
        )}
        {libraries.map((library) => (
          <option key={library.id} value={library.id}>
            {library.workspaceName} / {library.name}
          </option>
        ))}
      </Select>
      <Button
        size="sm"
        disabled={!canEdit || save.isPending || id === (current?.library_id ?? '')}
        onClick={() => save.mutate()}
      >
        Save inbox
      </Button>
    </>
  );
}
