import { useQuery } from '@tanstack/react-query';
import { Play } from 'lucide-react';
import { useState } from 'react';
import { workflows } from '@/api/queries';
import { useApiPages } from '@/api/pages';
import { Badge } from '@/components/ui/badge';
import { useAccess } from '@/features/lists/access';
import { useFormat } from '@/lib/preferences';
import type { Resource, Workflow, WorkflowRun } from '@/api/types';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/menu';
import { QueryFeedback } from '@/features/admin/common';
import { manualItemMode, type WorkflowDefinition } from './model';
import { LaunchWorkflow } from './workflow-tools';

/** Manual item workflows are offered only for the selected collection and cardinality. */
export function applicableWorkflows(entries: Workflow[], collectionId: string, count: number) {
  return entries.filter((workflow) => {
    const trigger = (workflow.definition as WorkflowDefinition).triggers.find((entry) => entry.type === 'manual');
    return workflow.enabled && trigger && manualItemMode(trigger) !== 'none'
      && (!trigger.collection_id || trigger.collection_id === collectionId)
      && count > 0 && count <= 100 && (trigger.selection === 'selection' || count === 1);
  });
}

export function RunWorkflowAction({ workspaceId, collectionId, items }: { workspaceId: string; collectionId: string; items: Resource[] }) {
  const [open, setOpen] = useState(false);
  const [launching, setLaunching] = useState<Workflow>();
  const list = useQuery({ queryKey: ['workflows', workspaceId], queryFn: () => workflows(workspaceId), enabled: open });
  const choices = applicableWorkflows(list.data ?? [], collectionId, items.length);
  return <>
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button size="sm" disabled={items.length === 0 || items.length > 100}><Play /> Run workflow</Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <QueryFeedback query={list} />
        {choices.map((workflow) => <DropdownMenuItem key={workflow.id} onSelect={() => setLaunching(workflow)}>{workflow.name}</DropdownMenuItem>)}
        {!choices.length && <DropdownMenuItem disabled>{list.isPending ? 'Loading workflows…' : 'No applicable workflows'}</DropdownMenuItem>}
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
    {canWrite && <section><h3 className="mb-2 font-medium">Run workflow</h3><RunWorkflowAction workspaceId={item.workspace_id} collectionId={collectionId} items={[item]} /></section>}
    <section className="grid gap-2">
      <h3 className="font-medium">Workflow history</h3>
      <QueryFeedback query={history} />
      <ul className="grid gap-2">{history.items.map((run) => <li key={run.id} className="rounded-md border p-3 text-sm">
        <div className="flex items-center justify-between gap-2"><span className="font-medium">{names.get(run.workflow_id) ?? run.workflow_id}</span><Badge tone={run.status === 'failed' ? 'danger' : run.status === 'completed' ? 'success' : 'neutral'}>{run.status}</Badge></div>
        <p className="text-xs text-muted">{run.event_type} · {format.dateTime(run.created_at)}{run.actor && ` · ${run.actor}`}</p>
        {run.updated_at && <p className="text-xs text-muted">Elapsed: {Math.max(0, Math.round((Date.parse(run.updated_at) - Date.parse(run.created_at)) / 1000))}s</p>}
        {canManage && <a className="text-xs text-accent underline" href={`/w/${item.workspace_id}/settings/runs?run=${run.id}`}>View steps{run.status === 'failed' ? ' and retry' : ''}</a>}
      </li>)}</ul>
      {history.isSuccess && !history.items.length && <p className="text-sm text-muted">No workflow runs for this item.</p>}
      {!canManage && <p className="text-xs text-muted">Workspace managers can inspect execution details and retry failed runs.</p>}
      {history.hasNextPage && <Button disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>More runs</Button>}
    </section>
  </div>;
}
