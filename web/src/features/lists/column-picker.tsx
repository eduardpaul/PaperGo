import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Columns3 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { ListingSort } from '@/api/requests';
import type { ListView, QuerySpec } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Checkbox } from '@/components/ui/select';
import { problemMessage } from '@/lib/errors';

/**
 * Chooses and orders the table's columns for this page (kept in the URL). People who manage the list can keep the
 * choice, with the current sort, in the open view or in a new view.
 */
export function ColumnPicker({
  collectionId,
  columns,
  available,
  changed,
  view,
  sort,
  canManage,
  onChange,
  onSaved,
}: {
  collectionId: string;
  columns: string[];
  available: { key: string; label: string }[];
  /** The columns differ from the view's (or the default ones). */
  changed: boolean;
  view?: ListView;
  sort?: ListingSort;
  canManage: boolean;
  onChange: (columns: string[] | undefined) => void;
  onSaved: (viewId: string) => void;
}) {
  const client = useQueryClient();
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState('');
  const label = (key: string) => available.find((entry) => entry.key === key)?.label ?? key;
  const ordered = [...columns, ...available.map((entry) => entry.key).filter((key) => !columns.includes(key))];
  const move = (index: number, by: number) => {
    const next = [...columns];
    const [column] = next.splice(index, 1);
    next.splice(index + by, 0, column);
    onChange(next);
  };
  const query = (base: QuerySpec | undefined): QuerySpec => ({ ...(base ?? {}), ...(sort ? { sort: [sort] } : {}) });
  const save = useMutation({
    meta: { silent: true },
    mutationFn: async (target: 'view' | 'new') => {
      if (target === 'view' && view) {
        await api(`/v1/views/${view.id}`, {
          method: 'PUT',
          ifMatch: view.version,
          body: { name: view.name, columns, layout: view.layout ?? 'table', is_default: !!view.is_default, query: query(view.query) },
        });
        return view.id;
      }
      const created = await api<ListView>(`/v1/resources/${collectionId}/views`, {
        method: 'POST',
        body: { name: name.trim(), columns, layout: 'table', is_default: false, query: query(view?.query) },
      });
      return created.data.id;
    },
    onSuccess: async (id, target) => {
      await client.invalidateQueries({ queryKey: keys.views(collectionId) });
      toast.success(target === 'view' ? 'View saved' : `View “${name.trim()}” created`);
      setNaming(false);
      setName('');
      onSaved(id);
    },
  });
  return (
    <Popover onOpenChange={(open) => !open && setNaming(false)}>
      <PopoverTrigger asChild>
        <Button size="sm" variant="ghost" aria-label={changed ? 'Columns (changed)' : 'Columns'}>
          <Columns3 /> Columns
          {changed && <span aria-hidden className="size-1.5 rounded-full bg-accent" />}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <p className="border-b px-3 py-2 text-xs font-medium text-muted">Show columns</p>
        <ul className="max-h-72 overflow-y-auto p-1">
          {ordered.map((key) => {
            const index = columns.indexOf(key);
            const shown = index >= 0;
            return (
              <li key={key} className="flex items-center gap-1 rounded px-2 py-1 hover:bg-surface-muted">
                <label className="flex flex-1 items-center gap-2 text-[13px]">
                  <Checkbox
                    checked={shown}
                    disabled={key === '$name'}
                    onChange={(event) =>
                      onChange(event.target.checked ? [...columns, key] : columns.filter((column) => column !== key))
                    }
                  />
                  {label(key)}
                </label>
                {shown && (
                  <>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-6"
                      aria-label={`Move ${label(key)} left`}
                      disabled={index === 0}
                      onClick={() => move(index, -1)}
                    >
                      <ArrowUp className="size-3.5" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-6"
                      aria-label={`Move ${label(key)} right`}
                      disabled={index === columns.length - 1}
                      onClick={() => move(index, 1)}
                    >
                      <ArrowDown className="size-3.5" />
                    </Button>
                  </>
                )}
              </li>
            );
          })}
        </ul>
        {(changed || canManage) && (
          <div className="flex flex-col gap-2 border-t p-2">
            {save.isError && <Alert>{problemMessage(save.error)}</Alert>}
            {naming ? (
              <form
                className="flex gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (name.trim()) save.mutate('new');
                }}
              >
                <Input
                  autoFocus
                  aria-label="Name of the new view"
                  placeholder="View name"
                  value={name}
                  maxLength={200}
                  onChange={(event) => setName(event.target.value)}
                />
                <Button type="submit" variant="primary" disabled={!name.trim() || save.isPending}>
                  Save
                </Button>
              </form>
            ) : (
              <div className="flex flex-wrap gap-2">
                {changed && (
                  <Button size="sm" variant="ghost" onClick={() => onChange(undefined)}>
                    Reset
                  </Button>
                )}
                {canManage && (changed || !!sort) && view && !view.system_key && (view.layout ?? 'table') === 'table' && (
                  <Button size="sm" disabled={save.isPending} onClick={() => save.mutate('view')}>
                    Save to “{view.name}”
                  </Button>
                )}
                {canManage && (
                  <Button size="sm" onClick={() => setNaming(true)}>
                    Save as new view…
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
