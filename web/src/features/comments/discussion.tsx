import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, MessageSquare, MoreHorizontal, RotateCcw } from 'lucide-react';
import { useRef, useState, type ReactNode } from 'react';
import { api } from '@/api/http';
import { commentsQuery, keys } from '@/api/queries';
import type { Comment, CommentPage, CommentPerson, CommentThread, MentionCandidate } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState, Skeleton } from '@/components/ui/feedback';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/menu';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';

const mentionToken = /<@user:([^<>\s]{1,255})>/g;

/** Writes a comment's mentions as @Name for editing, from the people it mentions. */
export function fromTokens(body: string, mentions: CommentPerson[]): string {
  return body.replace(
    mentionToken,
    (_, subject: string) => `@${mentions.find((m) => m.subject === subject)?.name ?? subject}`,
  );
}

/** Turns the @Name of each chosen person back into a mention, longest names first. */
export function toTokens(text: string, chosen: CommentPerson[]): string {
  let out = text;
  for (const person of [...chosen].sort((a, b) => b.name.length - a.name.length)) {
    out = out.split(`@${person.name}`).join(`<@user:${person.subject}>`);
  }
  return out;
}

/** A comment's text with its mentions shown as names. */
function Body({ comment }: { comment: Comment }) {
  if (comment.deleted) return <p className="text-[13px] text-muted italic">This comment was deleted.</p>;
  const parts: ReactNode[] = [];
  let last = 0;
  for (const match of comment.body.matchAll(mentionToken)) {
    parts.push(comment.body.slice(last, match.index));
    const person = comment.mentions.find((m) => m.subject === match[1]);
    parts.push(
      <span key={match.index} className="rounded bg-accent-soft px-1 text-accent">
        @{person?.name ?? match[1]}
      </span>,
    );
    last = match.index + match[0].length;
  }
  parts.push(comment.body.slice(last));
  return <p className="text-[13px] whitespace-pre-wrap break-words">{parts}</p>;
}

/** A text box that suggests people to mention after @. */
function Composer({
  itemId,
  initial = '',
  initialMentions = [],
  placeholder,
  submitLabel,
  pending,
  onSubmit,
  onCancel,
}: {
  itemId: string;
  initial?: string;
  initialMentions?: CommentPerson[];
  placeholder: string;
  submitLabel: string;
  pending: boolean;
  onSubmit: (body: string) => Promise<unknown>;
  onCancel?: () => void;
}) {
  const [text, setText] = useState(initial);
  const [chosen, setChosen] = useState<CommentPerson[]>(initialMentions);
  const [query, setQuery] = useState<string | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const candidates = useQuery({
    queryKey: ['mentionable', itemId, query],
    enabled: query !== null,
    queryFn: async () =>
      (await api<{ value: MentionCandidate[] }>(`/v1/items/${itemId}/mentionable`, { query: { q: query ?? '' } })).data
        .value,
  });
  const track = (value: string, caret: number) => {
    setText(value);
    const typed = /(?:^|\s)@([^\s@]*)$/.exec(value.slice(0, caret));
    setQuery(typed ? typed[1] : null);
  };
  const pick = (person: MentionCandidate) => {
    const caret = box.current?.selectionStart ?? text.length;
    const before = text.slice(0, caret).replace(/@([^\s@]*)$/, `@${person.name} `);
    setText(before + text.slice(caret));
    setChosen((list) => (list.some((p) => p.subject === person.subject) ? list : [...list, person]));
    setQuery(null);
    box.current?.focus();
  };
  const hidden = (candidates.data ?? []).filter((c) => !c.can_see);
  const unseen = chosen.filter((p) => hidden.some((h) => h.subject === p.subject) && text.includes(`@${p.name}`));
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        const body = toTokens(
          text.trim(),
          chosen.filter((p) => text.includes(`@${p.name}`)),
        );
        if (!body) return;
        void onSubmit(body).then(() => {
          setText('');
          setChosen([]);
        });
      }}
    >
      <div className="relative">
        <textarea
          ref={box}
          value={text}
          rows={3}
          maxLength={10000}
          placeholder={placeholder}
          aria-label={placeholder}
          className="w-full rounded-md border bg-surface px-3 py-2 text-[13px] shadow-xs outline-none focus:border-accent"
          onChange={(event) => track(event.target.value, event.target.selectionStart)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') setQuery(null);
          }}
        />
        {query !== null && (candidates.data?.length ?? 0) > 0 && (
          <ul
            className="absolute right-0 left-0 z-10 mt-1 max-h-48 overflow-y-auto rounded-md border bg-surface shadow-lg"
            aria-label="People to mention"
          >
            {candidates.data!.map((person) => (
              <li key={person.subject}>
                <button
                  type="button"
                  className="flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-surface-muted"
                  onClick={() => pick(person)}
                >
                  <span>
                    {person.name} <span className="text-xs text-muted">{person.subject}</span>
                  </span>
                  {!person.can_see && <span className="text-xs text-warning">can't see this item</span>}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {unseen.length > 0 && (
        <p className="text-xs text-warning">
          {unseen.map((p) => p.name).join(', ')} can't see this item and won't be told. A mention doesn't share it.
        </p>
      )}
      <div className="flex justify-end gap-2">
        {onCancel && (
          <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" size="sm" variant="primary" disabled={pending || !text.trim()}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

/** One comment: who, when, its text and what the reader may do with it. */
function CommentEntry({ comment, itemId, compact }: { comment: Comment; itemId: string; compact?: boolean }) {
  const format = useFormat();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.comments });
  const edit = useMutation({
    mutationFn: (body: string) =>
      api<Comment>(`/v1/comments/${comment.id}`, { method: 'PATCH', ifMatch: comment.version, body: { body } }),
    onSuccess: async () => {
      setEditing(false);
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: () => api(`/v1/comments/${comment.id}`, { method: 'DELETE', ifMatch: comment.version }),
    onSuccess: refresh,
  });
  return (
    <article
      className={cn('flex flex-col gap-1', compact && 'border-l-2 pl-3')}
      aria-label={`Comment by ${comment.author.name}`}
    >
      <header className="flex items-center gap-2 text-xs text-muted">
        <span className="font-medium text-foreground">{comment.author.name}</span>
        {comment.source === 'workflow' && <Badge>workflow</Badge>}
        <span>{format.dateTime(comment.created_at)}</span>
        {comment.edited_at && !comment.deleted && <span>(edited)</span>}
        {(comment.can_edit || comment.can_delete) && !editing && (
          <DropdownMenu>
            <DropdownMenuTrigger
              className="ml-auto rounded p-0.5 hover:bg-surface-muted"
              aria-label={`Actions for ${comment.author.name}'s comment`}
            >
              <MoreHorizontal className="size-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {comment.can_edit && <DropdownMenuItem onSelect={() => setEditing(true)}>Edit</DropdownMenuItem>}
              {comment.can_delete && <DropdownMenuItem onSelect={() => remove.mutate()}>Delete</DropdownMenuItem>}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </header>
      {editing ? (
        <Composer
          itemId={itemId}
          initial={fromTokens(comment.body, comment.mentions)}
          initialMentions={comment.mentions}
          placeholder="Edit the comment"
          submitLabel="Save"
          pending={edit.isPending}
          onSubmit={(body) => edit.mutateAsync(body)}
          onCancel={() => setEditing(false)}
        />
      ) : (
        <Body comment={comment} />
      )}
      {(edit.error || remove.error) && <Alert>{problemMessage(edit.error ?? remove.error)}</Alert>}
    </article>
  );
}

/** A thread: its first comment, its replies, and a reply box. */
function ThreadCard({ thread, itemId, canPost }: { thread: CommentThread; itemId: string; canPost: boolean }) {
  const queryClient = useQueryClient();
  const [replying, setReplying] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.comments });
  const reply = useMutation({
    mutationFn: (body: string) =>
      api<Comment>(`/v1/items/${itemId}/comments`, { method: 'POST', body: { body, thread_id: thread.id } }),
    onSuccess: async () => {
      setReplying(false);
      await refresh();
    },
  });
  const resolve = useMutation({
    mutationFn: () =>
      api<Comment>(`/v1/comments/${thread.id}/${thread.resolved_at ? 'reopen' : 'resolve'}`, {
        method: 'POST',
        ifMatch: thread.version,
      }),
    onSuccess: refresh,
  });
  return (
    <li className={cn('flex flex-col gap-3 rounded-lg border bg-surface p-3', thread.resolved_at && 'opacity-80')}>
      <CommentEntry comment={thread} itemId={itemId} />
      {thread.replies.map((r) => (
        <CommentEntry key={r.id} comment={r} itemId={itemId} compact />
      ))}
      {replying ? (
        <Composer
          itemId={itemId}
          placeholder="Reply"
          submitLabel="Reply"
          pending={reply.isPending}
          onSubmit={(body) => reply.mutateAsync(body)}
          onCancel={() => setReplying(false)}
        />
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          {canPost && !thread.deleted && (
            <Button size="sm" variant="ghost" onClick={() => setReplying(true)}>
              Reply
            </Button>
          )}
          {thread.can_resolve && (
            <Button size="sm" variant="ghost" disabled={resolve.isPending} onClick={() => resolve.mutate()}>
              {thread.resolved_at ? <RotateCcw className="size-4" /> : <CheckCircle2 className="size-4" />}
              {thread.resolved_at ? 'Reopen' : 'Resolve'}
            </Button>
          )}
          {thread.resolved_by && <span className="text-xs text-muted">Resolved by {thread.resolved_by.name}</span>}
        </div>
      )}
      {(reply.error || resolve.error) && <Alert>{problemMessage(reply.error ?? resolve.error)}</Alert>}
    </li>
  );
}

const statuses = [
  { value: 'open', label: 'Open' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
];

const origins: Record<CommentPage['mode_from'], string> = {
  deployment: 'this deployment',
  organization: 'the organization',
  workspace: 'this workspace',
  collection: 'this list or library',
  default: 'default',
};

/** An item's discussion: its threads, and a box to start one. */
export function Discussion({ itemId }: { itemId: string }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState('open');
  const page = useQuery(commentsQuery(itemId, status));
  const start = useMutation({
    mutationFn: (body: string) => api<Comment>(`/v1/items/${itemId}/comments`, { method: 'POST', body: { body } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.comments }),
  });
  if (page.isLoading) return <Skeleton className="h-24" />;
  if (page.error) return <Alert>{problemMessage(page.error)}</Alert>;
  const data = page.data!;
  if (data.mode === 'off') {
    return (
      <EmptyState icon={MessageSquare} title="Comments are off here">
        Turned off by {origins[data.mode_from]}.
      </EmptyState>
    );
  }
  if (!data.can_read) {
    return (
      <EmptyState icon={MessageSquare} title="The discussion is for collaborators">
        People who can read this item's drafts discuss it here.
      </EmptyState>
    );
  }
  const threads = data.value ?? [];
  return (
    <section className="flex flex-col gap-3" aria-label="Discussion">
      <div className="flex gap-2" role="tablist" aria-label="Thread views">
        {statuses.map((s) => (
          <Button
            key={s.value}
            role="tab"
            size="sm"
            aria-selected={status === s.value}
            variant={status === s.value ? 'primary' : 'secondary'}
            onClick={() => setStatus(s.value)}
          >
            {s.label}
          </Button>
        ))}
      </div>
      {threads.length === 0 && (
        <p className="py-4 text-center text-[13px] text-muted">
          {status === 'resolved' ? 'No resolved threads.' : 'No discussion yet.'}
        </p>
      )}
      <ul className="flex flex-col gap-3" aria-label="Threads">
        {threads.map((thread) => (
          <ThreadCard key={thread.id} thread={thread} itemId={itemId} canPost={data.can_post} />
        ))}
      </ul>
      {data.can_post ? (
        <Composer
          itemId={itemId}
          placeholder="Start a thread — type @ to mention someone"
          submitLabel="Comment"
          pending={start.isPending}
          onSubmit={(body) => start.mutateAsync(body)}
        />
      ) : (
        <p className="text-xs text-muted">You can read this discussion but not add to it.</p>
      )}
      {start.error && <Alert>{problemMessage(start.error)}</Alert>}
    </section>
  );
}
