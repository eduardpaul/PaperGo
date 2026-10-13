import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { bulkUpdateRequests } from '@/api/requests';
import type { FieldDefinition, QuerySpec } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Alert } from '@/components/ui/feedback';
import { problemMessage } from '@/lib/errors';
import { Button } from '@/components/ui/button';
import { SettingsSection } from '@/features/settings/section';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { QueryFeedback } from '@/features/admin/common';
import { OperationProgress } from '@/features/admin/operation-progress';
import type { Operation } from '@/features/admin/operation-progress';

export function BulkEditDialog({
  collectionId,
  target,
  count,
  fields,
  onClose,
  onStarted,
}: {
  collectionId: string;
  /** The selected items, or everything a query matches. */
  target: { ids: string[] } | { query: QuerySpec };
  /** How many items the query matches, as people should read it. */
  count?: ReactNode;
  fields: FieldDefinition[];
  onClose: () => void;
  onStarted: (operationIds: string[]) => void;
}) {
  const client = useQueryClient();
  const [key, setKey] = useState(fields[0]?.key ?? '');
  const [value, setValue] = useState<unknown>(null);
  const field = fields.find((field) => field.key === key);
  const apply = useMutation({
    mutationFn: async () => {
      const patch = valuesPatch([field!], {}, { [key]: value }) ?? { [key]: null };
      const started: string[] = [];
      for (const call of bulkUpdateRequests(collectionId, target, patch)) {
        started.push((await api<Operation>(call.path, { method: call.method, body: call.body })).data.id);
      }
      return started;
    },
    onSuccess: (started) => {
      void client.invalidateQueries({ queryKey: ['operations', collectionId, 'list'] });
      onStarted(started);
      onClose();
    },
  });
  const all = 'query' in target;
  return (
    <FormDialog
      open
      title={all ? <>Edit {count ?? 'all matching items'}</> : `Edit ${target.ids.length} selected items`}
      description={`Set one field on every ${all ? 'matching' : 'selected'} item in the background. Leave it empty to clear it. Each failure is reported separately.`}
      submitLabel={all ? 'Apply to all matching items' : 'Apply to selected items'}
      pending={apply.isPending}
      submitDisabled={!field}
      onClose={onClose}
      onSubmit={() => apply.mutate()}
    >
      {apply.isError && <Alert>{problemMessage(apply.error)}</Alert>}
      <Label htmlFor="bulk-field">Field</Label>
      <Select
        id="bulk-field"
        value={key}
        onChange={(event) => {
          setKey(event.target.value);
          setValue(null);
        }}
      >
        {fields.map((field) => (
          <option key={field.id} value={field.key}>
            {field.label}
          </option>
        ))}
      </Select>
      {field && <FieldEditor key={key} field={{ ...field, required: false }} value={value} onChange={setValue} />}
    </FormDialog>
  );
}

const operationKinds: Record<string, string> = {
  bulk_update: 'Field update on many items',
  field_index: 'Column index build',
};

/** Background work on a collection (bulk field updates, index builds), newest first. */
export function CollectionOperations({ collectionId }: { collectionId: string }) {
  const operations = useApiPages<Operation>(
    ['operations', collectionId, 'list'],
    `/v1/resources/${collectionId}/operations`,
  );
  const [selected, setSelected] = useState('');
  return (
    <SettingsSection
      title="Background work"
      description="Changes that run in the background, such as editing a field on many items or building a column index."
    >
      <QueryFeedback query={operations} />
      <ul className="flex flex-col divide-y">
        {operations.items.map((operation) => (
          <li key={operation.id}>
            <button
              type="button"
              className="flex w-full items-center gap-3 py-2 text-left text-[13px] hover:bg-surface-muted/50"
              aria-expanded={selected === operation.id}
              onClick={() => setSelected(selected === operation.id ? '' : operation.id)}
            >
              <span className="flex-1">{operationKinds[operation.kind] ?? operation.kind.replaceAll(/[_-]/g, ' ')}</span>
              <Badge
                tone={operation.status === 'succeeded' ? 'success' : operation.status === 'failed' ? 'danger' : 'neutral'}
              >
                {operation.status}
              </Badge>
            </button>
            {selected === operation.id && <OperationProgress id={operation.id} label="Progress" />}
          </li>
        ))}
      </ul>
      {!operations.isPending && operations.items.length === 0 && (
        <p className="text-sm text-muted">Nothing has run in the background yet.</p>
      )}
      {operations.hasNextPage && (
        <Button disabled={operations.isFetchingNextPage} onClick={() => void operations.fetchNextPage()}>
          Load more
        </Button>
      )}
    </SettingsSection>
  );
}
