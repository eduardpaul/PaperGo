import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { stringifyJSON } from '@/api/json';
import type { WorkflowRun, WorkflowRunStep } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { QueryFeedback } from '@/features/admin/common';
import { Trans, useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';
import { runStatusLabel } from './presentation';

const stepKinds = ['request', 'wait', 'value'] as const;
const active = ['queued', 'running', 'waiting'];

/** A run in a dialog. */
export function RunDetails({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useTranslation('workflows');
  return <Dialog open onOpenChange={(open) => !open && onClose()}><DialogContent className="max-h-[85vh] overflow-auto">
    <DialogHeader><DialogTitle>{t('run.detailsTitle')}</DialogTitle></DialogHeader>
    <div className="px-5 pb-5"><RunView id={id} /></div>
  </DialogContent></Dialog>;
}

/**
 * A run with the steps its script recorded, what it logged and its result,
 * refreshed every `interval` milliseconds while it is active.
 */
export function RunView({ id, interval = 3000 }: { id: string; interval?: number }) {
  const { t } = useTranslation('workflows');
  const client = useQueryClient();
  const format = useFormat();
  const query = useQuery({
    queryKey: ['workflow-runs', 'detail', id],
    queryFn: async () => (await api<WorkflowRun>(`/v1/workflow-runs/${id}`)).data,
    refetchInterval: (q) => (q.state.data && !active.includes(q.state.data.status) ? false : interval),
  });
  const act = useMutation({
    mutationFn: (action: 'retry' | 'cancel') => api(`/v1/workflow-runs/${id}/${action}`, { method: 'POST' }),
    onSuccess: async (_, action) => {
      toast.success(action === 'retry' ? t('run.retryQueued') : t('run.cancelled'));
      await client.invalidateQueries({ queryKey: ['workflow-runs'] });
    },
  });
  const run = query.data;
  return <div className="grid gap-3">
    <QueryFeedback query={query} />
    <QueryFeedback query={act} />
    {run && <>
      <p>{runStatusLabel(run.status)} · {run.event_type} · {run.actor}{run.workflow_version ? ` · ${t('run.version', { version: run.workflow_version })}` : ''}</p>
      {run.generation ? <p className="text-sm text-muted">{t('run.continued', { count: run.generation })}</p> : null}
      {run.state !== undefined && run.state !== null && <details><summary className="text-sm">{t('run.carriedState')}</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(run.state)}</pre></details>}
      {run.parent_run_id && <p className="text-sm text-muted"><Trans t={t} i18nKey="run.calledBy" components={{ link: <a className="text-accent underline" href={`/w/${run.workspace_id}/settings/runs?run=${encodeURIComponent(run.parent_run_id)}`} /> }} /></p>}
      {run.waiting_until && run.status === 'waiting' && <p className="text-sm text-muted">{t('run.continuesAt', { date: format.dateTime(run.waiting_until) })}</p>}
      {run.error && <pre className="overflow-auto whitespace-pre-wrap text-xs text-danger">{run.error}</pre>}
      <div className="flex gap-2">
        {run.status === 'failed' && <Button disabled={act.isPending} onClick={() => act.mutate('retry')}>{t('run.retry')}</Button>}
        {active.includes(run.status) && <Button disabled={act.isPending} onClick={() => act.mutate('cancel')}>{t('run.cancel')}</Button>}
      </div>
      {run.result !== undefined && run.result !== null && <details open><summary>{t('run.result')}</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(run.result)}</pre></details>}
      <h3 className="font-medium">{t('run.steps')}</h3>
      {!run.steps?.length && <p className="text-sm text-muted">{t('run.noSteps')}</p>}
      <ol className="grid gap-2">{run.steps?.map((step) => <Step key={step.seq} step={step} />)}</ol>
      {!!run.logs?.length && <>
        <h3 className="font-medium">{t('run.log')}</h3>
        <ol className="grid gap-1 font-mono text-xs">{run.logs.map((entry, index) => <li key={index} className={entry.level === 'error' ? 'text-danger' : entry.level === 'warn' ? 'text-warning' : undefined}>{format.dateTime(entry.at)} {entry.message}</li>)}</ol>
      </>}
    </>}
  </div>;
}

function Step({ step }: { step: WorkflowRunStep }) {
  const { t } = useTranslation('workflows');
  const format = useFormat();
  const response = step.kind === 'request';
  const output = response ? t('step.response') : t('step.output');
  const kind = stepKinds.find(entry => entry === step.kind);
  const kept = step.output !== undefined && step.output !== null;
  return <li className="rounded border p-2 text-sm">
    <p><strong>{kind ? t(`step.kind.${kind}`) : step.kind}</strong> · {step.name}{step.status === 'pending' ? ` · ${t('step.waiting')}` : step.status === 'failed' ? ` · ${t('step.failed')}` : ''}</p>
    {!!step.operations?.length && <p className="text-xs text-muted">{t('step.operations', { operations: step.operations.join(', ') })}</p>}
    {step.error && <p className="text-danger">{step.error}</p>}
    {step.input !== undefined && step.input !== null && <details><summary>{t('step.request')}</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(step.input)}</pre></details>}
    {kept && <details><summary>{output}{step.size ? ` · ${format.fileSize(step.size)}` : ''}</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(step.output)}</pre></details>}
    {!kept && !!step.size && <p className="text-xs text-muted">{response ? t('step.responseNotKept', { size: format.fileSize(step.size) }) : t('step.outputNotKept', { size: format.fileSize(step.size) })}</p>}
  </li>;
}
