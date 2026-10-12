import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { api } from '@/api/http';
import { downloadFile } from '@/api/files';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { QueryFeedback } from './common';

export interface Operation {
  id: string;
  kind: string;
  status: 'pending' | 'succeeded' | 'failed' | 'superseded';
  progress?: number;
  processed?: number;
  matched?: number;
  updated?: number;
  failed?: number;
  error?: string;
  failures?: { item_id: string; reason: string }[];
  result?: {
    rows?: number;
    objects?: number;
    valid?: boolean;
    dry_run?: boolean;
    errors?: string[];
    expires_at?: string;
  };
}
export function OperationProgress({ id, label }: { id: string; label: string }) {
  const client = useQueryClient();
  const completed = useRef('');
  const operation = useQuery({
    queryKey: ['operations', id],
    queryFn: async () => (await api<Operation>(`/v1/operations/${id}`)).data,
    refetchInterval: (query) => (!query.state.data || query.state.data.status === 'pending' ? 1000 : false),
  });
  const data = operation.data;
  useEffect(() => {
    if (data && data.status !== 'pending' && completed.current !== id) {
      completed.current = id;
      void client.invalidateQueries({
        predicate: (query) => query.queryKey[0] !== 'operations' || query.queryKey[2] === 'list',
      });
    }
  }, [data, id, client]);
  const download = useMutation({
    mutationFn: () => downloadFile(`/v1/portability/exports/${id}/package`, 'papergo-core.zip'),
  });
  const percent = data?.status === 'succeeded' ? 100 : (data?.progress ?? 0);
  return (
    <div role="status" aria-label={label} className="my-3 flex flex-col gap-2 rounded-md border p-3">
      <QueryFeedback query={operation} />
      {download.error && <QueryFeedback query={download} />}
      <div className="flex items-center gap-2">
        <span className="flex-1 font-medium">{label}</span>
        <Badge tone={data?.status === 'succeeded' ? 'success' : data?.status === 'failed' ? 'danger' : 'neutral'}>
          {data?.status ?? 'Loading'}
        </Badge>
      </div>
      <progress aria-label={`${label} progress`} value={percent} max={100} className="h-2 w-full accent-accent" />
      {data && (
        <p className="text-xs text-muted">
          {data.processed ?? 0} processed{data.updated ? ` · ${data.updated} updated` : ''}
          {data.failed ? ` · ${data.failed} failed` : ''}
        </p>
      )}
      <ul className="text-xs text-danger">
        {data?.failures?.map((failure) => (
          <li key={failure.item_id}>
            {failure.item_id}: {failure.reason}
          </li>
        ))}
      </ul>
      {data?.error && <p className="text-danger">{data.error}</p>}
      {data?.result && (
        <div className="text-xs">
          <p>
            {data.result.valid === false ? 'Package validation failed.' : data.result.valid ? 'Package is valid.' : ''}
            {data.result.dry_run && ' Validation only; content was not imported.'}
          </p>
          {data.result.rows !== undefined && (
            <p>
              {data.result.rows} records · {data.result.objects ?? 0} files
            </p>
          )}
          <ul>
            {data.result.errors?.map((error, index) => (
              <li key={index} className="text-danger">
                {error}
              </li>
            ))}
          </ul>
        </div>
      )}
      {data?.kind === 'portability.export' && data.status === 'succeeded' && (
        <Button disabled={download.isPending} onClick={() => download.mutate()}>
          Download package
        </Button>
      )}
      <span className="text-xs text-muted">Operation {id}</span>
    </div>
  );
}
