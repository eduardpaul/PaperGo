import { useQuery, useMutation } from '@tanstack/react-query';
import { Play } from 'lucide-react';
import { useState } from 'react';
import { workflows } from '@/api/queries';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { Badge } from '@/components/ui/badge';
import { useAccess } from '@/features/lists/access';
import { useFormat } from '@/lib/preferences';
import type { Resource, Workflow, WorkflowRun, WorkflowAction } from '@/api/types';
import { Select } from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/menu';
import { QueryFeedback } from '@/features/admin/common';
import { runOriginLabel, runStatusLabel } from './presentation';
import { LaunchWorkflow } from './workflow-tools';

export function RunWorkflowAction({ workspaceId, items }: { workspaceId: string; collectionId: string; items: Resource[] }) {
  const [open, setOpen] = useState(false);
  const [launching, setLaunching] = useState<Workflow>();
  const list = useQuery({ queryKey: ['workflow-actions', workspaceId, items.map(item => [item.id, item.version])], queryFn: async () => (await api<{ value: WorkflowAction[] }>(`/v1/workspaces/${workspaceId}/workflow-actions`, { method: 'POST', body: { item_ids: items.map(item => item.id) } })).data.value, enabled: open });
  const choices = list.data ?? [];
  return <>
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button size="sm" disabled={items.length === 0 || items.length > 100}><Play /> Run workflow</Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <QueryFeedback query={list} />
        {choices.map(action => <DropdownMenuItem key={action.workflow?.id ?? action.builtin?.key} disabled={!action.availability.can_run} onSelect={() => action.workflow && setLaunching(action.workflow)}><span className="grid gap-0.5"><span>{action.workflow?.name ?? action.builtin?.name}</span><span className="text-xs text-muted">{action.availability.can_run ? 'Run once · automatic settings stay unchanged' : action.availability.reason}</span></span></DropdownMenuItem>)}
        {!choices.length && <DropdownMenuItem disabled>{list.isPending ? 'Loading workflows…' : 'No workflows with a manual entry point'}</DropdownMenuItem>}
      </DropdownMenuContent>
    </DropdownMenu>
    {launching && <LaunchWorkflow workflow={launching} itemIds={items.map((item) => item.id)} fixedSelection={items} onClose={() => setLaunching(undefined)} />}
  </>;
}

/** Item history is refreshed by live events and by polling while the tab is open. */
export function ItemWorkflows({ item, collectionId, canWrite }: { item: Resource; collectionId: string; canWrite: boolean }) {
  const format = useFormat();
  const { canManage } = useAccess(item.workspace_id);
  const history = useApiPages<WorkflowRun>(['workflow-runs', item.workspace_id, 'item', item.id], `/v1/workspaces/${item.workspace_id}/workflow-runs`, { query: { item_id: item.id }, refetchInterval: 3000 });
  const definitions = useQuery({ queryKey: ['workflows', item.workspace_id], queryFn: () => workflows(item.workspace_id) });
  const names = new Map((definitions.data ?? []).map((entry) => [entry.id, entry.name]));
  return <div className="grid gap-4">
    {canWrite && <AutomaticCheck item={item} />}
    {canWrite && <section><h3 className="mb-2 font-medium">Run workflow</h3><RunWorkflowAction workspaceId={item.workspace_id} collectionId={collectionId} items={[item]} /></section>}
    <section className="grid gap-2">
      <h3 className="font-medium">Workflow history</h3>
      <QueryFeedback query={history} />
      <ul className="grid gap-2">{history.items.map((run) => <li key={run.id} className="rounded-md border p-3 text-sm">
        <div className="flex items-center justify-between gap-2"><span className="font-medium">{names.get(run.workflow_id) ?? run.workflow_id}</span><Badge tone={run.status === 'failed' ? 'danger' : run.status === 'completed' ? 'success' : 'neutral'}>{run.outcome === 'unchanged' ? 'No changes needed' : runStatusLabel(run.status)}</Badge></div>
        <p className="text-xs text-muted">{runOriginLabel(run.event_type)} · {format.dateTime(run.created_at)}{run.actor && ` · ${run.actor}`}</p>
        {run.updated_at && <p className="text-xs text-muted">Elapsed: {Math.max(0, Math.round((Date.parse(run.updated_at) - Date.parse(run.created_at)) / 1000))}s</p>}
        {canManage && <a className="text-xs text-accent underline" href={`/w/${item.workspace_id}/settings/runs?run=${run.id}`}>View steps{run.status === 'failed' ? ' and retry' : ''}</a>}
      </li>)}</ul>
      {history.isSuccess && !history.items.length && <p className="text-sm text-muted">No workflow runs for this item.</p>}
      {!canManage && <p className="text-xs text-muted">Workspace managers can inspect execution details and retry failed runs.</p>}
      {history.hasNextPage && <Button disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>More runs</Button>}
    </section>
  </div>;
}

function AutomaticCheck({ item }: { item: Resource }) {
  const [event, setEvent] = useState('file.updated');
  const check = useMutation({ mutationFn: async () => {
      const entries = await workflows(item.workspace_id);
      const results = await Promise.all(entries.map(async workflow => {
        const response = await api<{ reason: string }>(`/v1/workflows/${workflow.id}/explain`, { method: 'POST', body: { item_id: item.id, event_type: event, source: 'upload' } });
        return `${workflow.name}: ${response.data.reason}`;
      }));
      return results;
  } });
  return <details><summary className="cursor-pointer text-sm font-medium">Explain automatic behavior</summary><div className="mt-2 grid gap-2"><label className="text-sm">Check an event<Select aria-label="Automatic event" value={event} onChange={event => { setEvent(event.target.value); check.reset(); }}><option value="file.updated">File replaced by upload</option><option value="file.added">File added by upload</option><option value="item.updated">Metadata updated</option></Select></label><p className="text-xs text-muted">A read-only eligibility check. No events or workflow runs are created.</p><div><Button size="sm" disabled={check.isPending} onClick={() => check.mutate()}>Check this file</Button></div><QueryFeedback query={{isPending:check.isPending,error:check.error}} />{check.data && <ul className="grid gap-2 text-xs" aria-live="polite">{check.data.map((text,index) => <li key={index}>{text}</li>)}</ul>}</div></details>;
}
