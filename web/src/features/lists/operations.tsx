import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import type { FieldDefinition } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { QueryFeedback } from '@/features/admin/common';
import { OperationProgress } from '@/features/admin/operation-progress';
import type { Operation } from '@/features/admin/operation-progress';

export function BulkEditDialog({
  collectionId,
  ids,
  fields,
  onClose,
  onStarted,
}: {
  collectionId: string;
  ids: string[];
  fields: FieldDefinition[];
  onClose: () => void;
  onStarted: (id: string) => void;
}) {
  const client = useQueryClient();
  const [key, setKey] = useState(fields[0]?.key ?? '');
  const [value, setValue] = useState<unknown>(null);
  const field = fields.find((field) => field.key === key);
  const apply = useMutation({
    mutationFn: () => {
      const patch = valuesPatch([field!], {}, { [key]: value }) ?? { [key]: null };
      return api<Operation>(`/v1/resources/${collectionId}/bulk-update`, {
        method: 'POST',
        body: { query: { filter: { field: '$id', op: 'in', value: ids } }, values: patch },
      });
    },
    onSuccess: (result) => {
      void client.invalidateQueries({ queryKey: ['operations', collectionId, 'list'] });
      onStarted(result.data.id);
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={`Edit ${ids.length} selected items`}
      description="Set one field on every selected item in the background. Leave it empty to clear it. Each failure is reported separately."
      submitLabel="Apply to selected items"
      pending={apply.isPending}
      submitDisabled={!field}
      onClose={onClose}
      onSubmit={() => apply.mutate()}
    >
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

export function CollectionOperations({ collectionId }: { collectionId: string }) {
  const operations = useApiPages<Operation>(
    ['operations', collectionId, 'list'],
    `/v1/resources/${collectionId}/operations`,
  );
  const [selected, setSelected] = useState('');
  return (
    <details className="my-3 rounded border p-3">
      <summary className="cursor-pointer text-sm font-medium">Background operations</summary>
      <QueryFeedback query={operations} />
      <ul>
        {operations.items.map((operation) => (
          <li key={operation.id}>
            <button
              type="button"
              className="py-1 text-left text-sm hover:underline"
              onClick={() => setSelected(operation.id)}
            >
              {operation.kind} · {operation.status} · {operation.id}
            </button>
          </li>
        ))}
      </ul>
      {!operations.isPending && operations.items.length === 0 && <p className="text-sm text-muted">No operations.</p>}
      {operations.hasNextPage && (
        <Button disabled={operations.isFetchingNextPage} onClick={() => void operations.fetchNextPage()}>
          Load more operations
        </Button>
      )}
      {selected && <OperationProgress id={selected} label="Operation progress" />}
    </details>
  );
}
