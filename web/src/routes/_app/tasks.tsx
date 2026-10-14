import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { ClipboardCheck, Plus } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { myTasksQuery, taskQuery, workspacesQuery } from '@/api/queries';
import type { TaskFilter } from '@/api/queries';
import type { Task } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Page, PageHeader } from '@/components/page';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState, Skeleton } from '@/components/ui/feedback';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { TaskDetail, outcomeLabel } from '@/features/tasks/detail';
import type { FormSchema } from '@/features/workflows/model';
import { normalizeForm } from '@/features/workflows/model';
import { SchemaFields } from '@/features/workflows/schema-form';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { textParam } from '@/lib/search';
import { cn } from '@/lib/utils';

interface TasksSearch {
  view?: string;
  id?: string;
}

export const Route = createFileRoute('/_app/tasks')({
  validateSearch: (search: Record<string, unknown>): TasksSearch => ({
    view: textParam(search.view),
    id: textParam(search.id),
  }),
  component: TasksPage,
});

const views: { value: string; label: string; filter: TaskFilter }[] = [
  { value: 'waiting', label: 'Waiting for me', filter: { waiting: true } },
  { value: 'approvals', label: 'Approvals', filter: { waiting: true, kind: 'approval' } },
  { value: 'completed', label: 'Completed', filter: { status: 'completed' } },
  { value: 'all', label: 'All', filter: {} },
];

function TasksPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const view = views.find((v) => v.value === search.view) ?? views[0];
  const list = useInfiniteQuery(myTasksQuery(view.filter));
  const rows = list.data?.pages.flatMap((page) => page.value ?? []) ?? [];
  const selectedId = search.id ?? rows[0]?.id;
  const [creating, setCreating] = useState(false);
  return (
    <Page wide>
      <PageHeader
        title="Tasks"
        description="Approvals and other requests waiting for you."
        icon={ClipboardCheck}
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-4" /> New task
          </Button>
        }
      />
      <div className="mb-4 flex flex-wrap gap-2" role="tablist" aria-label="Task views">
        {views.map((v) => (
          <Button
            key={v.value}
            role="tab"
            aria-selected={view.value === v.value}
            size="sm"
            variant={view.value === v.value ? 'primary' : 'secondary'}
            onClick={() => navigate({ search: { view: v.value } })}
          >
            {v.label}
          </Button>
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-[20rem_1fr]">
        <div className="flex flex-col gap-3">
          {list.isLoading && <Skeleton className="h-16" />}
          {list.error && <Alert>{problemMessage(list.error)}</Alert>}
          <ul className="flex flex-col gap-1" aria-label="Tasks">
            {rows.map((task) => (
              <li key={task.id}>
                <TaskRow
                  task={task}
                  selected={task.id === selectedId}
                  onSelect={() => navigate({ search: { view: view.value, id: task.id } })}
                />
              </li>
            ))}
          </ul>
          {list.hasNextPage && (
            <Button disabled={list.isFetching} onClick={() => void list.fetchNextPage()}>
              {list.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </Button>
          )}
        </div>
        {selectedId ? (
          <Detail key={selectedId} id={selectedId} />
        ) : list.isSuccess && rows.length === 0 ? (
          <EmptyState
            icon={ClipboardCheck}
            title={view.filter.waiting ? 'Nothing is waiting for you' : 'No tasks yet'}
          />
        ) : null}
      </div>
      {creating && <CreateTask onClose={() => setCreating(false)} onCreated={(id) => navigate({ search: { view: 'all', id } })} />}
    </Page>
  );
}

function TaskRow({ task, selected, onSelect }: { task: Task; selected: boolean; onSelect: () => void }) {
  const format = useFormat();
  const overdue = task.status === 'open' && task.due_at && new Date(task.due_at) < new Date();
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected}
      className={cn(
        'w-full rounded-md border px-3 py-2 text-left hover:bg-surface-muted',
        selected && 'border-accent bg-accent-soft',
      )}
    >
      <p className="truncate text-[13px] font-medium">{task.title}</p>
      <p className="flex flex-wrap items-center gap-1 text-xs text-muted">
        {task.kind === 'approval' && <Badge>approval</Badge>}
        {task.status === 'completed' ? (
          outcomeLabel(task, task.outcome)
        ) : task.due_at ? (
          <span className={cn(overdue && 'text-danger')}>due {format.dateTime(task.due_at)}</span>
        ) : (
          format.dateTime(task.created_at)
        )}
      </p>
    </button>
  );
}

function Detail({ id }: { id: string }) {
  const task = useQuery(taskQuery(id));
  if (task.error) return <Alert>{problemMessage(task.error)}</Alert>;
  if (!task.data) return <Skeleton className="h-96" />;
  return <TaskDetail task={task.data} />;
}

/** The new-task form, rendered by the same form system as workflow launch forms. */
const newTaskSchema: FormSchema = {
  type: 'object',
  required: ['title'],
  properties: {
    title: { type: 'string', title: 'Title', maxLength: 500 },
    description: { type: 'string', title: 'Description', maxLength: 4000 },
    assignees: {
      type: 'array',
      title: 'Assignees',
      description: 'None means the workspace owners.',
      items: { type: 'string' },
      maxItems: 50,
      'x-papergo': { kind: 'people', access: 'read' },
    },
    priority: { type: 'string', title: 'Priority', enum: ['low', 'normal', 'high'], default: 'normal' },
    due: { type: 'string', title: 'Due date', format: 'date' },
  },
};

function CreateTask({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const queryClient = useQueryClient();
  const workspaces = useQuery(workspacesQuery);
  const [workspaceId, setWorkspaceId] = useState('');
  const [values, setValues] = useState<Record<string, unknown>>({ title: '', priority: 'normal' });
  const workspace = workspaceId || workspaces.data?.[0]?.id || '';
  const create = useMutation({
    mutationFn: () => {
      const { due, ...form } = normalizeForm(newTaskSchema, values) as Record<string, unknown>;
      return api<Task>(`/v1/workspaces/${workspace}/tasks`, {
        method: 'POST',
        body: { ...form, due_at: due ? `${String(due)}T00:00:00Z` : undefined },
      });
    },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: ['tasks'] });
      onCreated(result.data.id);
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title="New task"
      submitLabel="Create task"
      pending={create.isPending}
      submitDisabled={!workspace || !String(values.title ?? '').trim()}
      onClose={onClose}
      onSubmit={() => create.mutate()}
    >
      <Label htmlFor="task-workspace">Workspace</Label>
      <Select id="task-workspace" value={workspace} onChange={(event) => setWorkspaceId(event.target.value)}>
        {workspaces.data?.map((w) => (
          <option key={w.id} value={w.id}>
            {w.name}
          </option>
        ))}
      </Select>
      {workspace && <SchemaFields schema={newTaskSchema} data={values} onChange={setValues} workspaceId={workspace} />}
      {create.error && <Alert>{problemMessage(create.error)}</Alert>}
    </FormDialog>
  );
}
