import { useQuery } from '@tanstack/react-query';
import { Play } from 'lucide-react';
import { useState } from 'react';
import { workflows } from '@/api/queries';
import type { Resource, Workflow } from '@/api/types';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/menu';
import { QueryFeedback } from '@/features/admin/common';
import type { WorkflowDefinition } from './model';
import { LaunchWorkflow } from './workflow-tools';

/** Manual item workflows are offered only for the selected collection and cardinality. */
export function applicableWorkflows(entries: Workflow[], collectionId: string, count: number) {
  return entries.filter((workflow) => {
    const trigger = (workflow.definition as WorkflowDefinition).triggers.find((entry) => entry.type === 'manual');
    return workflow.enabled && trigger && (trigger.collection_id || trigger.selection)
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
