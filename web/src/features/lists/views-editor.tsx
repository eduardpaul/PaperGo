import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Pencil, Plus } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import { keys, permissions } from '@/api/queries';
import type { ContentType, FieldDefinition, ListView, QuerySpec } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input, Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { columnLabel } from './schema';
import { FilterEditor, normalizeFilter, systemFields } from './filter-editor';

export function ViewsEditor({
  collectionId,
  fields,
  contentTypes,
}: {
  collectionId: string;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
}) {
  const client = useQueryClient();
  const access = useQuery({
    queryKey: ['permissions', collectionId],
    queryFn: () => permissions(collectionId),
    retry: false,
  });
  const canManage = !!access.data;
  const views = useQuery({
    queryKey: keys.views(collectionId),
    queryFn: () => allPages<ListView>(`/v1/resources/${collectionId}/views`),
  });
  const [editing, setEditing] = useState<ListView | 'new'>();
  const [deleting, setDeleting] = useState<ListView>();
  const remove = useMutation({
    mutationFn: (view: ListView) => api(`/v1/views/${view.id}`, { method: 'DELETE', ifMatch: view.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: keys.resources });
    },
  });
  return (
    <>
      <SettingsSection
        title="Saved views"
        description="Columns, filtering, sorting, grouping and layout for everyone using this collection."
        actions={
          canManage && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> New view
            </Button>
          )
        }
      >
        <QueryFeedback query={views} />
        <ul className="divide-y">
          {views.data?.map((view) => (
            <li key={view.id} className="flex items-center gap-3 py-3">
              <span className="flex-1 font-medium">
                {view.name} <Badge>{view.layout}</Badge>
                {view.is_default && <Badge className="ml-2">Default</Badge>}
              </span>
              <Button onClick={() => setEditing(view)}>
                <Pencil /> {canManage ? 'Edit' : 'View'}
              </Button>
              {canManage && (
                <Button variant="danger" onClick={() => setDeleting(view)}>
                  Delete
                </Button>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      {editing && (
        <ViewDialog
          collectionId={collectionId}
          fields={fields}
          contentTypes={contentTypes}
          view={editing === 'new' ? undefined : editing}
          canManage={canManage}
          onClose={() => setEditing(undefined)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={`Delete ${deleting?.name ?? ''}?`}
        description="Items remain; this saved view is removed."
        confirm="Delete view"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

function ViewDialog({
  collectionId,
  fields,
  contentTypes,
  view,
  canManage,
  onClose,
}: {
  collectionId: string;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  view?: ListView;
  canManage: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState(view?.name ?? '');
  const [columns, setColumns] = useState(view?.columns ?? ['$name']);
  const [layout, setLayout] = useState(view?.layout ?? 'table');
  const [isDefault, setIsDefault] = useState(!!view?.is_default);
  const [query, setQuery] = useState<QuerySpec>(view?.query ?? {});
  const queryable = [
    ...systemFields,
    ...fields.filter((field) => field.indexed && (!field.index_status || field.index_status === 'ready')),
  ];
  const sortable = queryable.filter((field) => field.key !== '$tags' && !field.options?.multiple);
  const grouping =
    layout === 'board'
      ? sortable.filter((field) => field.type === 'choice')
      : layout === 'calendar'
        ? sortable.filter((field) => ['date', 'datetime'].includes(field.type))
        : sortable;
  const validLayout = !['board', 'calendar'].includes(layout) || grouping.some((field) => field.key === query.group_by);
  const patchQuery = (patch: Partial<QuerySpec>) => setQuery((current) => ({ ...current, ...patch }));
  const save = useMutation({
    mutationFn: () =>
      api(view ? `/v1/views/${view.id}` : `/v1/resources/${collectionId}/views`, {
        method: view ? 'PUT' : 'POST',
        ifMatch: view?.version,
        body: {
          name: name.trim(),
          columns,
          layout,
          is_default: isDefault,
          query: { ...query, filter: normalizeFilter(query.filter, queryable) },
        },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  const move = (index: number, by: number) =>
    setColumns((current) => {
      const next = [...current];
      const [column] = next.splice(index, 1);
      next.splice(index + by, 0, column);
      return next;
    });
  return (
    <FormDialog
      open
      title={view ? `View ${view.name}` : 'New view'}
      submitLabel="Save view"
      submitDisabled={!canManage || columns.length === 0 || !validLayout}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <fieldset disabled={!canManage} className="grid gap-3">
        <Label htmlFor="view-name">Name</Label>
        <Input id="view-name" required value={name} onChange={(event) => setName(event.target.value)} />
        <Label htmlFor="view-layout">Layout</Label>
        <Select
          id="view-layout"
          value={layout}
          onChange={(event) => {
            setLayout(event.target.value);
            patchQuery({ group_by: undefined });
          }}
        >
          {['table', 'board', 'calendar', 'gallery'].map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </Select>
        <label className="flex gap-2">
          <Checkbox checked={isDefault} onChange={(event) => setIsDefault(event.target.checked)} />
          Default view
        </label>
        <fieldset>
          <legend className="font-medium">Columns</legend>
          <ul>
            {[
              ...columns,
              ...[...systemFields, ...fields].map((field) => field.key).filter((key) => !columns.includes(key)),
            ].map((key) => {
              const index = columns.indexOf(key);
              return (
                <li key={key} className="flex items-center gap-2 py-1">
                  <label className="flex flex-1 gap-2">
                    <Checkbox
                      checked={index >= 0}
                      onChange={(event) =>
                        setColumns((current) =>
                          event.target.checked ? [...current, key] : current.filter((value) => value !== key),
                        )
                      }
                    />
                    {columnLabel(key, fields)}
                  </label>
                  {index >= 0 && (
                    <>
                      <Button
                        size="icon"
                        aria-label={`Move ${key} up`}
                        disabled={index === 0}
                        onClick={() => move(index, -1)}
                      >
                        <ArrowUp />
                      </Button>
                      <Button
                        size="icon"
                        aria-label={`Move ${key} down`}
                        disabled={index === columns.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown />
                      </Button>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        </fieldset>
        <Label htmlFor="view-type">Content type</Label>
        <Select
          id="view-type"
          value={query.content_type_id ?? ''}
          onChange={(event) => patchQuery({ content_type_id: event.target.value || undefined })}
        >
          <option value="">All types</option>
          {contentTypes.map((type) => (
            <option key={type.id} value={type.id}>
              {type.name}
            </option>
          ))}
        </Select>
        <Label htmlFor="view-group">Group or calendar field</Label>
        <Select
          id="view-group"
          value={query.group_by ?? ''}
          onChange={(event) => patchQuery({ group_by: event.target.value || undefined })}
        >
          <option value="">None</option>
          {grouping.map((field) => (
            <option key={field.key} value={field.key}>
              {field.label}
            </option>
          ))}
        </Select>
        <fieldset>
          <legend className="mb-2 font-medium">Sort</legend>
          {(query.sort ?? []).map((sort, index) => (
            <div key={index} className="mb-2 flex gap-2">
              <Select
                aria-label={`Sort field ${index + 1}`}
                value={sort.field}
                onChange={(event) =>
                  patchQuery({
                    sort: query.sort!.map((current, i) =>
                      i === index ? { ...current, field: event.target.value } : current,
                    ),
                  })
                }
              >
                {sortable.map((field) => (
                  <option key={field.key} value={field.key}>
                    {field.label}
                  </option>
                ))}
              </Select>
              <Select
                aria-label={`Sort direction ${index + 1}`}
                value={sort.direction}
                onChange={(event) =>
                  patchQuery({
                    sort: query.sort!.map((current, i) =>
                      i === index ? { ...current, direction: event.target.value } : current,
                    ),
                  })
                }
              >
                <option value="asc">Ascending</option>
                <option value="desc">Descending</option>
              </Select>
              <Button onClick={() => patchQuery({ sort: query.sort!.filter((_, i) => i !== index) })}>
                Remove sort
              </Button>
            </div>
          ))}
          <Button
            onClick={() => patchQuery({ sort: [...(query.sort ?? []), { field: '$modified_at', direction: 'desc' }] })}
          >
            Add sort
          </Button>
        </fieldset>
        <FilterEditor filter={query.filter} fields={queryable} onChange={(filter) => patchQuery({ filter })} />
        <Label htmlFor="view-search">Search text</Label>
        <Input
          id="view-search"
          value={query.search ?? ''}
          onChange={(event) => patchQuery({ search: event.target.value || undefined })}
        />
        <Label htmlFor="view-tag">Tag</Label>
        <Input
          id="view-tag"
          value={query.tag ?? ''}
          onChange={(event) => patchQuery({ tag: event.target.value || undefined })}
        />
        <Label htmlFor="view-parent">Folder</Label>
        <Input
          id="view-parent"
          value={query.parent_id ?? ''}
          placeholder="Folder id, or empty for all items"
          onChange={(event) => patchQuery({ parent_id: event.target.value || undefined })}
        />
      </fieldset>
    </FormDialog>
  );
}
