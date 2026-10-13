import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { resourceQuery } from '@/api/queries';
import type { Approval } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Label, Textarea } from '@/components/ui/input';
import { SchemaFields } from '@/features/workflows/schema-form';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { Review } from './review';

/** An approval request with its review and, while it is open, the decision form. */
export function ApprovalDetail({ approval }: { approval: Approval }) {
  const format = useFormat();
  return (
    <article
      className="grid min-w-0 gap-4 rounded-lg border p-4"
      aria-label={approval.title}
    >
      <header className="grid gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold">{approval.title}</h2>
          <Badge
            tone={
              approval.status === "open"
                ? "neutral"
                : approval.status === "approved"
                  ? "success"
                  : "danger"
            }
          >
            {approval.status}
          </Badge>
        </div>
        <p className="text-xs text-muted">
          Requested {format.dateTime(approval.created_at)}
          {approval.decided_by &&
            ` · decided by ${approval.decided_by} ${format.dateTime(approval.decided_at)}`}
        </p>
        {approval.description && (
          <p className="text-[13px] whitespace-pre-line">
            {approval.description}
          </p>
        )}
        {approval.item_id && (
          <ItemLink
            workspaceId={approval.workspace_id}
            itemId={approval.item_id}
          />
        )}
      </header>
      <Review approval={approval} />
      {approval.status === "open" ? (
        <DecisionForm approval={approval} />
      ) : (
        approval.comment && <p className="text-[13px]">“{approval.comment}”</p>
      )}
    </article>
  );
}

/** Items open in their list or library. */
function ItemLink({
  workspaceId,
  itemId,
}: {
  workspaceId: string;
  itemId: string;
}) {
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

function DecisionForm({ approval }: { approval: Approval }) {
  const queryClient = useQueryClient();
  const [input, setInput] = useState<Record<string, unknown>>({});
  const [comment, setComment] = useState("");
  const decide = useMutation({
    mutationFn: (decision: "approve" | "reject") =>
      api<Approval>(`/v1/approvals/${approval.id}/decision`, {
        method: "POST",
        body: {
          decision,
          comment: comment || undefined,
          input: approval.input_schema ? input : undefined,
        },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["approvals"] }),
  });
  return (
    <form
      className="grid gap-3"
      onSubmit={(event) => event.preventDefault()}
      aria-label="Decision"
    >
      {approval.input_schema && (
        <SchemaFields
          schema={approval.input_schema}
          data={input}
          onChange={setInput}
          workspaceId={approval.workspace_id}
        />
      )}
      <div className="grid gap-1">
        <Label htmlFor="approval-comment">Comment</Label>
        <Textarea
          id="approval-comment"
          value={comment}
          maxLength={4000}
          onChange={(event) => setComment(event.target.value)}
        />
      </div>
      {decide.error && <Alert>{problemMessage(decide.error)}</Alert>}
      <div className="flex gap-2">
        <Button
          variant="primary"
          disabled={decide.isPending}
          onClick={() => decide.mutate("approve")}
        >
          Approve
        </Button>
        <Button
          variant="danger"
          disabled={decide.isPending}
          onClick={() => decide.mutate("reject")}
        >
          Reject
        </Button>
      </div>
    </form>
  );
}
