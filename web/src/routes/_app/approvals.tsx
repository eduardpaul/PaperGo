import { useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { ClipboardCheck } from 'lucide-react';
import { approvalQuery, myApprovalsQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState, Skeleton } from '@/components/ui/feedback';
import { ApprovalDetail } from '@/features/approvals/detail';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { textParam } from '@/lib/search';
import { cn } from '@/lib/utils';

interface ApprovalsSearch {
  status?: string;
  id?: string;
}

export const Route = createFileRoute("/_app/approvals")({
  validateSearch: (search: Record<string, unknown>): ApprovalsSearch => ({
    status: textParam(search.status),
    id: textParam(search.id),
  }),
  component: ApprovalsPage,
});

const filters = [
  { value: "open", label: "Waiting for me" },
  { value: "approved", label: "Approved" },
  { value: "rejected", label: "Rejected" },
] as const;

function ApprovalsPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const status = search.status ?? "open";
  const list = useQuery(myApprovalsQuery(status));
  const rows = list.data ?? [];
  const selectedId = search.id ?? rows[0]?.id;
  const format = useFormat();
  return (
    <Page wide>
      <PageHeader
        title="Approvals"
        description="Decisions workflows are waiting for."
        icon={ClipboardCheck}
      />
      <div
        className="mb-4 flex flex-wrap gap-2"
        role="tablist"
        aria-label="Approval status"
      >
        {filters.map((filter) => (
          <Button
            key={filter.value}
            role="tab"
            aria-selected={status === filter.value}
            size="sm"
            variant={status === filter.value ? "primary" : "secondary"}
            onClick={() => navigate({ search: { status: filter.value } })}
          >
            {filter.label}
          </Button>
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-[20rem_1fr]">
        <ul className="flex flex-col gap-1" aria-label="Approval requests">
          {list.isLoading && <Skeleton className="h-16" />}
          {list.error && <Alert>{problemMessage(list.error)}</Alert>}
          {rows.map((approval) => (
            <li key={approval.id}>
              <button
                type="button"
                onClick={() =>
                  navigate({ search: { status, id: approval.id } })
                }
                aria-current={approval.id === selectedId}
                className={cn(
                  "w-full rounded-md border px-3 py-2 text-left hover:bg-surface-muted",
                  approval.id === selectedId && "border-accent bg-accent-soft",
                )}
              >
                <p className="truncate text-[13px] font-medium">
                  {approval.title}
                </p>
                <p className="text-xs text-muted">
                  {format.dateTime(approval.created_at)}
                </p>
              </button>
            </li>
          ))}
        </ul>
        {list.isFetched && rows.length === 0 ? (
          <EmptyState
            icon={ClipboardCheck}
            title={
              status === "open"
                ? "Nothing is waiting for you"
                : "No decisions yet"
            }
          />
        ) : selectedId ? (
          <Detail key={selectedId} id={selectedId} />
        ) : null}
      </div>
    </Page>
  );
}

function Detail({ id }: { id: string }) {
  const approval = useQuery(approvalQuery(id));
  if (approval.error) return <Alert>{problemMessage(approval.error)}</Alert>;
  if (!approval.data) return <Skeleton className="h-96" />;
  return <ApprovalDetail approval={approval.data} />;
}
