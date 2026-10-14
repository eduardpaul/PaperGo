import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { resourceQuery } from '@/api/queries';
import type { Task, TaskOutcome } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Label, Textarea } from '@/components/ui/input';
import type { FormSchema } from '@/features/workflows/model';
import { normalizeForm } from '@/features/workflows/model';
import { SchemaFields } from '@/features/workflows/schema-form';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { Review } from './review';

const statusTone = {
  open: 'accent',
  completed: 'success',
  cancelled: 'neutral',
  expired: 'warning',
} as const;

/** The label of an outcome key, as the task names it. */
export function outcomeLabel(task: Task, key?: string) {
  return task.outcomes.find((o) => o.key === key)?.label ?? key ?? '';
}

/**
 * A task of any kind: what it asks, its review, the form and one button per
 * outcome while the reader may answer, and the answers given so far.
 */
export function TaskDetail({ task }: { task: Task }) {
  const format = useFormat();
  return (
    <article className="grid min-w-0 gap-4 rounded-lg border p-4" aria-label={task.title}>
      <header className="grid gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold">{task.title}</h2>
          <Badge tone={statusTone[task.status]}>
            {task.status === 'completed' ? outcomeLabel(task, task.outcome) : task.status}
          </Badge>
          {task.kind === 'approval' && <Badge>approval</Badge>}
          {task.priority === 'high' && <Badge tone="danger">high priority</Badge>}
        </div>
        <p className="text-xs text-muted">
          Requested {format.dateTime(task.created_at)}
          {task.source === 'workflow' ? ' by a workflow' : ` by ${task.created_by}`}
          {task.due_at && ` · due ${format.dateTime(task.due_at)}`}
          {task.completion === 'all' && ' · every assignee answers'}
        </p>
        {task.description && <p className="text-[13px] whitespace-pre-line">{task.description}</p>}
        {task.item_id && <ItemLink workspaceId={task.workspace_id} itemId={task.item_id} />}
      </header>
      <Review task={task} />
      {task.can_respond && <ResponseForm task={task} />}
      {task.can_reassign && <Reassign task={task} />}
      {task.answered && !task.can_respond && task.status === 'open' && (
        <p className="text-[13px] text-muted">You answered; the task waits for the other assignees.</p>
      )}
      <Answers task={task} />
    </article>
  );
}

/** Items open in their list or library. */
function ItemLink({ workspaceId, itemId }: { workspaceId: string; itemId: string }) {
  const item = useQuery(resourceQuery(itemId));
  if (!item.data?.container_id) return null;
  return (
    <Link
      to="/w/$workspaceId/l/$listId"
      params={{ workspaceId, listId: item.data.container_id }}
      search={{ item: itemId }}
      className="text-[13px] text-accent hover:underline"
    >
      Open {item.data.name}
    </Link>
  );
}

function ResponseForm({ task }: { task: Task }) {
  const queryClient = useQueryClient();
  const [input, setInput] = useState<Record<string, unknown>>(() => ({ ...task.values }));
  const [comment, setComment] = useState('');
  const [missing, setMissing] = useState<string>();
  const respond = useMutation({
    mutationFn: (outcome: TaskOutcome) =>
      api<Task>(`/v1/tasks/${task.id}/responses`, {
        method: 'POST',
        body: {
          outcome: outcome.key,
          // A note typed while weighing another outcome is not this one's.
          comment: outcome.comment === 'none' ? undefined : comment.trim() || undefined,
          input: task.input_schema && !outcome.skip_form ? normalizeForm(task.input_schema, input) : undefined,
        },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tasks'] }),
  });
  const commentable = task.outcomes.some((o) => o.comment !== 'none');
  const answer = (outcome: TaskOutcome) => {
    if (outcome.comment === 'required' && !comment.trim()) {
      setMissing(`${outcome.label} needs a comment.`);
      return;
    }
    setMissing(undefined);
    respond.mutate(outcome);
  };
  return (
    <form className="grid gap-3" onSubmit={(event) => event.preventDefault()} aria-label="Answer">
      {task.input_schema && (
        <SchemaFields schema={task.input_schema} data={input} onChange={setInput} workspaceId={task.workspace_id} />
      )}
      {commentable && (
        <div className="grid gap-1">
          <Label htmlFor="task-comment">Comment</Label>
          <Textarea
            id="task-comment"
            value={comment}
            maxLength={4000}
            onChange={(event) => setComment(event.target.value)}
          />
        </div>
      )}
      {missing && <Alert>{missing}</Alert>}
      {respond.error && <Alert>{problemMessage(respond.error)}</Alert>}
      <div className="flex flex-wrap gap-2">
        {task.outcomes.map((outcome) => (
          <Button
            key={outcome.key}
            variant={outcome.style === 'primary' ? 'primary' : outcome.style === 'danger' ? 'danger' : 'secondary'}
            disabled={respond.isPending}
            onClick={() => answer(outcome)}
          >
            {outcome.label}
          </Button>
        ))}
      </div>
    </form>
  );
}

const addPeopleSchema: FormSchema = {
  type: 'object',
  properties: {
    people: {
      type: 'array',
      title: 'Add people',
      items: { type: 'string' },
      maxItems: 50,
      'x-papergo': { kind: 'people', access: 'read' },
    },
  },
};

/**
 * Owners change who a task waits for, such as when an assignee left: they
 * keep or drop each assignee and add people.
 */
function Reassign({ task }: { task: Task }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [kept, setKept] = useState<string[]>(task.assignees);
  const [added, setAdded] = useState<Record<string, unknown>>({});
  const reassign = useMutation({
    mutationFn: () => {
      const people = Array.isArray(added.people) ? (added.people as string[]) : [];
      return api<Task>(`/v1/tasks/${task.id}/reassign`, {
        method: 'POST',
        body: { assignees: [...new Set([...kept, ...people])] },
      });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['tasks'] });
      setOpen(false);
    },
  });
  if (!open) {
    return (
      <div>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          Reassign
        </Button>
      </div>
    );
  }
  return (
    <form
      className="grid gap-3 rounded-md border p-3"
      aria-label="Reassign"
      onSubmit={(event) => {
        event.preventDefault();
        reassign.mutate();
      }}
    >
      <fieldset className="grid gap-1">
        <legend className="text-[13px] font-medium">Assignees</legend>
        {task.assignees.map((a) => (
          <label key={a} className="flex items-center gap-2 text-[13px]">
            <input
              type="checkbox"
              checked={kept.includes(a)}
              onChange={(event) => setKept(event.target.checked ? [...kept, a] : kept.filter((k) => k !== a))}
            />
            {a}
          </label>
        ))}
      </fieldset>
      <SchemaFields schema={addPeopleSchema} data={added} onChange={setAdded} workspaceId={task.workspace_id} />
      <p className="text-xs text-muted">
        Answers of assignees you keep count; with every assignee answering, the task completes once everyone left has
        answered. No assignees means the workspace owners.
      </p>
      {reassign.error && <Alert>{problemMessage(reassign.error)}</Alert>}
      <div className="flex gap-2">
        <Button type="submit" disabled={reassign.isPending}>
          Save assignees
        </Button>
        <Button variant="secondary" onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

/** The answers given, each with its form values, read only. */
function Answers({ task }: { task: Task }) {
  const format = useFormat();
  if (!task.responses?.length) return null;
  return (
    <section className="grid gap-2" aria-label="Answers">
      <h3 className="text-[13px] font-semibold">Answers</h3>
      <ul className="grid gap-2">
        {task.responses.map((r) => (
          <li key={r.assignee} className="rounded-md border p-3 text-[13px]">
            <p>
              <span className="font-medium">{r.subject}</span>
              {r.assignee !== r.subject && <span className="text-muted"> for {r.assignee}</span>}
              {': '}
              {outcomeLabel(task, r.outcome)}
              <span className="text-xs text-muted"> · {format.dateTime(r.created_at)}</span>
            </p>
            {r.comment && <p className="whitespace-pre-line">“{r.comment}”</p>}
            {r.input && task.input_schema && <FormValues schema={task.input_schema} values={r.input} />}
          </li>
        ))}
      </ul>
    </section>
  );
}

function FormValues({ schema, values }: { schema: FormSchema; values: Record<string, unknown> }) {
  const entries = Object.entries(schema.properties ?? {}).filter(([key]) => values[key] != null && values[key] !== '');
  if (entries.length === 0) return null;
  const show = (v: unknown) => (Array.isArray(v) ? v.join(', ') : typeof v === 'object' ? JSON.stringify(v) : String(v));
  return (
    <dl className="mt-1 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-0.5">
      {entries.map(([key, property]) => (
        <div key={key} className="contents">
          <dt className="text-muted">{property.title ?? key}</dt>
          <dd className="min-w-0 truncate">{show(values[key])}</dd>
        </div>
      ))}
    </dl>
  );
}
