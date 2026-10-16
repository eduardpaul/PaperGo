import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { stringifyJSON } from '@/api/json';
import type { WorkflowRun } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { QueryFeedback } from '@/features/admin/common';

export function RunDetails({ id, onClose }: { id: string; onClose: () => void }) {
  const client = useQueryClient();
  const query = useQuery({ queryKey: ['workflow-runs', 'detail', id], queryFn: async () => (await api<WorkflowRun>(`/v1/workflow-runs/${id}`)).data, refetchInterval: 3000 });
  const retry = useMutation({ mutationFn: () => api(`/v1/workflow-runs/${id}/retry`, { method: 'POST' }), onSuccess: async () => { toast.success('Retry queued'); await client.invalidateQueries({ queryKey: ['workflow-runs'] }); } });
  return <Dialog open onOpenChange={(open) => !open && onClose()}><DialogContent className="max-h-[85vh] overflow-auto">
    <DialogHeader><DialogTitle>Workflow run details</DialogTitle></DialogHeader>
    <div className="grid gap-3 px-5 pb-5">
      <QueryFeedback query={query} />
      <QueryFeedback query={retry} />
      {query.data && <>
        <p>{query.data.status} · {query.data.event_type} · {query.data.actor}</p>
        {query.data.error && <p className="text-danger">{query.data.error}</p>}
        {query.data.status === 'failed' && <Button disabled={retry.isPending} onClick={() => retry.mutate()}>Retry run</Button>}
        <ol className="grid gap-2">{query.data.steps?.map((step) => <li key={step.id} className="rounded border p-2 text-sm"><strong>{step.name}</strong>{step.error && <p className="text-danger">{step.error}</p>}{step.output !== undefined && <details><summary>Step output</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(step.output)}</pre></details>}</li>)}</ol>
      </>}
    </div>
  </DialogContent></Dialog>;
}
