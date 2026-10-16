import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { api } from '@/api/http';
import { workflowRuns, workflows } from '@/api/queries';
import type { WorkflowRun } from '@/api/types';
import { Button } from '@/components/ui/button';
import { SettingsSection } from '@/features/settings/section';
import { RunDetails } from '@/features/workflows/run-details';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/runs')({ component: RunsPage, validateSearch: (search: Record<string, unknown>): { run?: string } => ({ run: typeof search.run === 'string' ? search.run : undefined }) });

function RunsPage() {
  const { workspaceId } = Route.useParams();
  const { run: selectedRun } = Route.useSearch();
  const navigate = Route.useNavigate();
  const format = useFormat();
  const queryClient = useQueryClient();
  const runs = useQuery({ queryKey: ['workflow-runs', workspaceId], queryFn: () => workflowRuns(workspaceId) });
  const definitions = useQuery({ queryKey: ['workflows', workspaceId], queryFn: () => workflows(workspaceId) });
  const names = new Map((definitions.data ?? []).map((workflow) => [workflow.id, workflow.name]));
  const act = useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'cancel' | 'retry' }) => api(`/v1/workflow-runs/${id}/${action}`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['workflow-runs', workspaceId] }),
  });
  const rows = runs.data?.value ?? [];
  return (
    <SettingsSection title="Runs" description="Recent workflow runs in this workspace.">
      <ul className="flex flex-col gap-2">
        {rows.map((run) => (
          <li key={run.id} className="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2">
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-medium">{names.get(run.workflow_id) ?? run.workflow_id}</p>
              <p className="text-xs text-muted">
                {run.status} · {format.dateTime(run.created_at)}
                {run.error ? ` · ${run.error}` : ''}
              </p>
            </div>
            <Button size="sm" onClick={() => void navigate({ search: { run: run.id } })}>View steps</Button>
            <RunActions run={run} pending={act.isPending} onAct={(action) => act.mutate({ id: run.id, action })} />
          </li>
        ))}
        {runs.isFetched && rows.length === 0 && <li className="text-sm text-muted">No runs yet.</li>}
      </ul>
      {selectedRun && <RunDetails id={selectedRun} onClose={() => void navigate({ search: { run: undefined } })} />}
    </SettingsSection>
  );
}

function RunActions({ run, pending, onAct }: { run: WorkflowRun; pending: boolean; onAct: (action: 'cancel' | 'retry') => void }) {
  if (run.status === 'queued' || run.status === 'running') {
    return (
      <Button size="sm" disabled={pending} onClick={() => onAct('cancel')}>
        Cancel
      </Button>
    );
  }
  if (run.status === 'failed') {
    return (
      <Button size="sm" disabled={pending} onClick={() => onAct('retry')}>
        Retry
      </Button>
    );
  }
  return null;
}
