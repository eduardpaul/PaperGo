import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { api } from '@/api/http';
import { workflows } from '@/api/queries';
import type { WorkflowRun } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Checkbox, Select } from '@/components/ui/select';
import { useState } from 'react';
import { useApiPages } from '@/api/pages';
import { QueryFeedback } from '@/features/admin/common';
import { runStatusLabel, runOriginLabel } from '@/features/workflows/presentation';
import { SettingsSection } from '@/features/settings/section';
import { RunDetails } from '@/features/workflows/run-details';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';

const runStatuses = ['queued', 'running', 'waiting', 'completed', 'failed', 'cancelled'];

export const Route = createFileRoute('/_app/w/$workspaceId/settings/runs')({ component: RunsPage, validateSearch: (search: Record<string, unknown>): { run?: string } => ({ run: typeof search.run === 'string' ? search.run : undefined }) });

function RunsPage() {
  const { workspaceId } = Route.useParams();
  const { t } = useTranslation('workflows');
  const { run: selectedRun } = Route.useSearch();
  const navigate = Route.useNavigate();
  const format = useFormat();
  const queryClient = useQueryClient();
  const [workflowId, setWorkflowId] = useState('');
  const [origin, setOrigin] = useState('');
  const [status, setStatus] = useState('');
  const [includeSystem, setIncludeSystem] = useState(false);
  const runs = useApiPages<WorkflowRun>(['workflow-runs', workspaceId], `/v1/workspaces/${workspaceId}/workflow-runs`, { query: { workflow_id: workflowId || undefined, origin: origin || undefined, status: status || undefined, include_system: includeSystem ? 'true' : undefined }, refetchInterval: 3000 });
  const definitions = useQuery({ queryKey: ['workflows', workspaceId], queryFn: () => workflows(workspaceId) });
  const names = new Map((definitions.data ?? []).map((workflow) => [workflow.id, workflow.name]));
  const act = useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'cancel' | 'retry' }) => api(`/v1/workflow-runs/${id}/${action}`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['workflow-runs', workspaceId] }),
  });
  const rows = runs.items;
  return (
    <SettingsSection title={t('runs.title')} description={t('runs.description')}>
      <div className="mb-4 grid gap-2 sm:grid-cols-3">
        <Select aria-label={t('runs.workflow')} value={workflowId} onChange={event => setWorkflowId(event.target.value)}><option value="">{t('runs.allWorkflows')}</option>{definitions.data?.map(entry => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</Select>
        <Select aria-label={t('runs.origin')} value={origin} onChange={event => setOrigin(event.target.value)}><option value="">{t('runs.allOrigins')}</option><option value="manual">{t('run.origin.manual')}</option><option value="automatic">{t('runs.automatic')}</option><option value="schedule">{t('run.origin.schedule')}</option><option value="test">{t('run.origin.test')}</option><option value="call">{t('runs.called')}</option></Select>
        <Select aria-label={t('runs.status')} value={status} onChange={event => setStatus(event.target.value)}><option value="">{t('runs.allStatuses')}</option>{runStatuses.map(status => <option key={status} value={status}>{runStatusLabel(status)}</option>)}</Select>
      </div>
      <label className="mb-3 flex items-center gap-2 text-sm"><Checkbox checked={includeSystem} onChange={event => setIncludeSystem(event.target.checked)} />{t('runs.includeSystem')}</label>
      <QueryFeedback query={runs} />
      <ul className="flex flex-col gap-2">
        {rows.map((run) => (
          <li key={run.id} className="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2">
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-medium">{names.get(run.workflow_id) ?? run.workflow_id}</p>
              <p className="text-xs text-muted">
                {runStatusLabel(run.status)} · {runOriginLabel(run.event_type)} · {format.dateTime(run.created_at)}
                {run.error ? ` · ${run.error}` : ''}
              </p>
            </div>
            <Button size="sm" onClick={() => void navigate({ search: { run: run.id } })}>{t('item.viewSteps')}</Button>
            <RunActions run={run} pending={act.isPending} onAct={(action) => act.mutate({ id: run.id, action })} />
          </li>
        ))}
        {runs.isFetched && rows.length === 0 && <li className="text-sm text-muted">{t('runs.none')}</li>}
      </ul>
      {runs.hasNextPage && <Button disabled={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()}>{t('moreRuns')}</Button>}
      {selectedRun && <RunDetails id={selectedRun} onClose={() => void navigate({ search: { run: undefined } })} />}
    </SettingsSection>
  );
}

function RunActions({ run, pending, onAct }: { run: WorkflowRun; pending: boolean; onAct: (action: 'cancel' | 'retry') => void }) {
  const { t } = useTranslation();
  if (run.status === 'queued' || run.status === 'running' || run.status === 'waiting') {
    return (
      <Button size="sm" disabled={pending} onClick={() => onAct('cancel')}>
        {t('actions.cancel')}
      </Button>
    );
  }
  if (run.status === 'failed') {
    return (
      <Button size="sm" disabled={pending} onClick={() => onAct('retry')}>
        {t('actions.retry')}
      </Button>
    );
  }
  return null;
}
