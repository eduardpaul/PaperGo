import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { bulkUpdateRequests } from '@/api/requests';
import type { FieldDefinition, QuerySpec } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Alert } from '@/components/ui/feedback';
import { useTranslation } from '@/i18n';
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
  title,
  fields,
  onClose,
  onStarted,
}: {
  collectionId: string;
  /** The selected items, or everything a query matches. */
  target: { ids: string[] } | { query: QuerySpec };
  /** The title for everything a query matches, with how many it matches once that is known. */
  title?: ReactNode;
  fields: FieldDefinition[];
  onClose: () => void;
  onStarted: (operationIds: string[]) => void;
}) {
  const { t } = useTranslation('lists');
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
      title={all ? (title ?? t('bulkEdit.editAll')) : t('bulkEdit.editSelected', { count: target.ids.length })}
      description={all ? t('bulkEdit.descriptionAll') : t('bulkEdit.descriptionSelected')}
      submitLabel={all ? t('bulkEdit.applyAll') : t('bulkEdit.applySelected')}
      pending={apply.isPending}
      submitDisabled={!field}
      onClose={onClose}
      onSubmit={() => apply.mutate()}
    >
      {apply.isError && <Alert>{problemMessage(apply.error)}</Alert>}
      <Label htmlFor="bulk-field">{t('bulkEdit.field')}</Label>
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

const operationKinds = { bulk_update: 'bulk_update', 'integration.refresh': 'integration_refresh', field_index: 'field_index' } as const;
const operationStatuses: string[] = ['pending', 'succeeded', 'failed', 'superseded'] satisfies Operation['status'][];

/** Background work on a collection (bulk field updates, index builds), newest first. */
export function CollectionOperations({ collectionId }: { collectionId: string }) {
  const { t } = useTranslation(['lists', 'common']);
  const operations = useApiPages<Operation>(
    ['operations', collectionId, 'list'],
    `/v1/resources/${collectionId}/operations`,
  );
  const [selected, setSelected] = useState('');
  return (
    <SettingsSection
      title={t('operations.title')}
      description={t('operations.description')}
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
              <span className="flex-1">{operation.kind in operationKinds ? t(`operations.kinds.${operationKinds[operation.kind as keyof typeof operationKinds]}`) : operation.kind.replaceAll(/[_-]/g, ' ')}</span>
              <Badge
                tone={operation.status === 'succeeded' ? 'success' : operation.status === 'failed' ? 'danger' : 'neutral'}
              >
                {operationStatuses.includes(operation.status) ? t(`operations.status.${operation.status}`) : operation.status}
              </Badge>
            </button>
            {selected === operation.id && <OperationProgress id={operation.id} label={t('operations.progress')} />}
          </li>
        ))}
      </ul>
      {!operations.isPending && operations.items.length === 0 && (
        <p className="text-sm text-muted">{t('operations.none')}</p>
      )}
      {operations.hasNextPage && (
        <Button disabled={operations.isFetchingNextPage} onClick={() => void operations.fetchNextPage()}>
          {t('loadMore')}
        </Button>
      )}
    </SettingsSection>
  );
}
