import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api, continuation } from '@/api/http';
import { keys, querySmartFolder, smartFolder, smartFolderGroups } from '@/api/queries';
import type { Page, SmartFolder, SmartFolderEntry, SmartFolderGroup } from '@/api/types';
import { Page as Screen, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { ClassifyDialog } from './classification';
import { ConfirmDialog } from '@/features/settings/section';
import { QueryFeedback } from '@/features/admin/common';
import { useFormat } from '@/lib/preferences';

export function SmartFolderPage({ folderId }: { folderId: string }) {
  const navigate = useNavigate();
  const format = useFormat();
  const queryClient = useQueryClient();
  const folder = useQuery({ queryKey: ['smart-folders', folderId], queryFn: () => smartFolder(folderId) });
  const [classifying, setClassifying] = useState(false);
  const [unclassifying, setUnclassifying] = useState<SmartFolderEntry>();
  const unclassify = useMutation({
    mutationFn: (entry: SmartFolderEntry) =>
      api(`/v1/smart-folders/${folderId}/items/${entry.item.id}`, {
        method: 'DELETE',
        ifMatch: entry.item.version,
        body: { folder_version: folder.data!.version },
      }),
    onSuccess: async () => {
      setUnclassifying(undefined);
      await queryClient.invalidateQueries({ queryKey: keys.smartFolders });
      await queryClient.invalidateQueries({ queryKey: keys.resources });
    },
  });
  const [seenFolder, setSeenFolder] = useState(folderId);
  const [path, setPath] = useState<(string | null)[]>([]);
  if (folderId !== seenFolder) {
    setSeenFolder(folderId);
    setPath([]);
  }
  const definition = folder.data?.definition;
  const grouped = path.length < (definition?.group_by?.length ?? 0);

  const groups = useQuery({
    queryKey: ['smart-folders', folderId, 'groups', path],
    enabled: !!folder.data && grouped,
    queryFn: () => smartFolderGroups(folderId, path),
  });
  const entries = useQuery({
    queryKey: ['smart-folders', folderId, 'entries', path],
    enabled: !!folder.data && !grouped,
    queryFn: () => querySmartFolder(folderId, path),
  });
  const [entrySnapshot, setEntrySnapshot] = useState(entries.data);
  const [more, setMore] = useState<SmartFolderEntry[]>([]);
  const [next, setNext] = useState<string | null>(continuation(entries.data?.['@odata.nextLink']));
  if (entries.data !== entrySnapshot) {
    setEntrySnapshot(entries.data);
    setMore([]);
    setNext(continuation(entries.data?.['@odata.nextLink']));
  }

  async function loadMore() {
    if (!next) return;
    const page = (await api<Page<SmartFolderEntry>>(next, { method: 'POST', body: { include_total: true, path } }))
      .data;
    setMore((current) => [...current, ...(page.value ?? [])]);
    setNext(continuation(page['@odata.nextLink']));
  }

  if (folder.isPending) {
    return (
      <Screen>
        <Skeleton className="h-10 w-64" />
      </Screen>
    );
  }
  if (!folder.data)
    return (
      <Screen>
        <EmptyState title="Smart folder not found" />
      </Screen>
    );

  const rows = [...(entries.data?.value ?? []), ...more];
  return (
    <Screen>
      <PageHeader
        title={folder.data.name}
        description={
          folder.data.description || (folder.data.owner_id ? 'Personal smart folder' : 'Shared smart folder')
        }
        actions={
          <Button variant="primary" onClick={() => setClassifying(true)}>
            Add item
          </Button>
        }
      />
      <FolderDetails
        key={`${folder.data.id}:${folder.data.version}`}
        folder={folder.data}
        onSaved={async () => {
          await queryClient.invalidateQueries({ queryKey: keys.smartFolders });
          await queryClient.invalidateQueries({ queryKey: ['smart-folders', folderId] });
        }}
        onDeleted={async () => {
          await queryClient.invalidateQueries({ queryKey: keys.smartFolders });
          void navigate({ to: '/' });
        }}
      />
      <nav className="mb-3 flex flex-wrap items-center gap-2 text-xs" aria-label="Group path">
        <button type="button" className="text-muted hover:text-foreground" onClick={() => setPath([])}>
          All
        </button>
        {path.map((segment, index) => (
          <span key={`${segment ?? 'empty'}-${index}`} className="flex items-center gap-2">
            <span className="text-muted">/</span>
            <button type="button" className="hover:underline" onClick={() => setPath(path.slice(0, index + 1))}>
              {segment ?? '(empty)'}
            </button>
          </span>
        ))}
      </nav>
      <QueryFeedback query={grouped ? groups : entries} />
      {grouped ? (
        <GroupList
          groups={groups.data?.value ?? []}
          pending={groups.isPending}
          onOpen={(group) => setPath([...path, group.value == null ? null : String(group.value)])}
        />
      ) : (
        <ul className="overflow-hidden rounded-lg border bg-surface">
          {entries.isPending && (
            <li className="p-4">
              <Skeleton className="h-8" />
            </li>
          )}
          {rows.map((entry) => (
            <li key={entry.item.id} className="flex items-center border-b last:border-0">
              <button
                type="button"
                className="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-surface-muted"
                onClick={() =>
                  void navigate({
                    to: '/w/$workspaceId/l/$listId',
                    params: { workspaceId: entry.workspace_id, listId: entry.collection_id },
                    search: entry.item.kind === 'folder' ? { folder: entry.item.id } : { item: entry.item.id },
                  })
                }
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{entry.item.name}</span>
                  <span className="text-xs text-muted">{entry.collection_name}</span>
                </span>
                <span className="text-xs text-muted">
                  {entry.item.updated_at ? format.date(entry.item.updated_at) : ''}
                </span>
              </button>
              {entry.item.kind === 'item' && (
                <Button size="sm" className="mr-3" onClick={() => setUnclassifying(entry)}>
                  Remove classification
                </Button>
              )}
            </li>
          ))}
          {!entries.isPending && rows.length === 0 && <EmptyState title="No matching items" />}
        </ul>
      )}
      {!grouped && entries.data?.total != null && (
        <p className="mt-2 text-xs text-muted">{entries.data.total} matches</p>
      )}
      {next && (
        <Button className="mt-3" onClick={() => void loadMore()}>
          Load more
        </Button>
      )}
      {classifying && <ClassifyDialog folder={folder.data} path={path} onClose={() => setClassifying(false)} />}
      <ConfirmDialog
        open={!!unclassifying}
        onOpenChange={(open) => !open && setUnclassifying(undefined)}
        title={`Remove ${unclassifying?.item.name ?? ''} from this smart folder?`}
        description="Clear the folder’s term and equality classifications. The item remains in its collection. Definitions that still match after clearing cannot be removed this way."
        confirm="Remove classification"
        busy={unclassify.isPending}
        error={unclassify.error}
        onConfirm={() => unclassifying && unclassify.mutate(unclassifying)}
      />
    </Screen>
  );
}

function FolderDetails({
  folder,
  onSaved,
  onDeleted,
}: {
  folder: SmartFolder;
  onSaved: () => Promise<void>;
  onDeleted: () => Promise<void>;
}) {
  const [name, setName] = useState(folder.name);
  const [description, setDescription] = useState(folder.description ?? '');
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/smart-folders/${folder.id}`, {
        method: 'PUT',
        ifMatch: folder.version,
        body: {
          name: name.trim(),
          description: description.trim(),
          personal: !!folder.owner_id,
          workspace_id: folder.workspace_id,
          definition: folder.definition ?? {},
        },
      }),
    onSuccess: async () => {
      await onSaved();
      toast.success('Smart folder saved');
    },
  });
  const remove = useMutation({
    mutationFn: () => api(`/v1/smart-folders/${folder.id}`, { method: 'DELETE', ifMatch: folder.version }),
    onSuccess: () => onDeleted(),
  });
  return (
    <form
      className="mb-4 grid gap-2 rounded-lg border bg-surface p-4 sm:grid-cols-[1fr_1fr_auto_auto]"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div>
        <Label htmlFor="folder-name">Name</Label>
        <Input id="folder-name" className="mt-1" value={name} onChange={(event) => setName(event.target.value)} />
      </div>
      <div>
        <Label htmlFor="folder-description">Description</Label>
        <Input
          id="folder-description"
          className="mt-1"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </div>
      <Button type="submit" className="self-end" disabled={save.isPending}>
        Save
      </Button>
      <Button
        type="button"
        variant="danger"
        className="self-end"
        disabled={remove.isPending}
        onClick={() => remove.mutate()}
      >
        Delete
      </Button>
    </form>
  );
}

function GroupList({
  groups,
  pending,
  onOpen,
}: {
  groups: SmartFolderGroup[];
  pending: boolean;
  onOpen: (group: SmartFolderGroup) => void;
}) {
  if (pending) return <Skeleton className="h-16" />;
  if (groups.length === 0) return <EmptyState title="No groups" />;
  return (
    <ul className="overflow-hidden rounded-lg border bg-surface">
      {groups.map((group) => (
        <li key={String(group.value)} className="border-b last:border-0">
          <button
            type="button"
            className="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-surface-muted"
            onClick={() => onOpen(group)}
          >
            <span className="flex-1 truncate text-sm font-medium">
              {group.label || (group.value == null ? '(empty)' : String(group.value))}
            </span>
            <span className="text-xs text-muted">{group.count}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
