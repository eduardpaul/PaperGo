import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { allPages, useApiPages } from '@/api/pages';
import type { Resource, Workflow, WorkflowAvailability } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Alert } from '@/components/ui/feedback';
import { problemMessage } from '@/lib/errors';
import { QueryFeedback } from '@/features/admin/common';
import { FilePreview } from '@/features/documents/preview';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';
import { formDefaults, manualItemMode, normalizeForm } from './model';
import type { WorkflowDefinition } from './model';
import { SchemaFields } from './schema-form';
import { automaticSummary, workspaceTypesQuery } from './presentation';

export function LaunchWorkflow({
  workflow,
  onClose,
  itemIds: initialItems = [],
  fixedSelection,
}: {
  workflow: Workflow;
  onClose: () => void;
  itemIds?: string[];
  fixedSelection?: Resource[];
}) {
  const { t } = useTranslation('workflows');
  const client = useQueryClient();
  const definition = workflow.definition as WorkflowDefinition;
  const types = useQuery(workspaceTypesQuery(workflow.workspace_id, definition.triggers));
  const manual = definition.triggers.find((trigger) => trigger.type === 'manual');
  const itemMode = manualItemMode(manual);
  const schema = definition.input_schema ?? { type: 'object', properties: {} };
  const hints = schema['x-papergo-selection'];
  const [inputs, setInputs] = useState<Record<string, unknown>>(formDefaults(schema));
  const [itemIds, setItemIds] = useState(initialItems);
  const [primary, setPrimary] = useState(initialItems[0] ?? '');
  const [q, setQ] = useState('');
  const items = useApiPages<Resource>(
    ['resources', workflow.workspace_id, manual?.collection_id, 'launch-items', q],
    manual?.collection_id ? `/v1/resources/${manual.collection_id}/query` : '/v1/resources',
    {
      enabled: !!manual && itemMode !== 'none' && !fixedSelection,
      query: manual?.collection_id ? undefined : { workspace_id: workflow.workspace_id, q, surface: 'head' },
      body: manual?.collection_id ? { surface: 'head', query: { search: q || undefined } } : undefined,
    },
  );
  const options = (fixedSelection ?? items.items)
    .filter((item) => item.kind === 'item')
    .map((item) => ({ value: item.id, label: item.name }));
  const launchBody = { workflow_version: workflow.current_version, item_ids: itemIds.length ? itemIds : undefined, primary_item_id: manual?.selection === 'selection' ? (itemIds.includes(primary) ? primary : itemIds[0]) : undefined, inputs: normalizeForm(schema, inputs) };
  const check = useQuery({ queryKey: ['workflow-launch-check', workflow.id, workflow.version, launchBody], queryFn: async () => (await api<WorkflowAvailability>(`/v1/workflows/${workflow.id}/check`, { method: 'POST', body: launchBody })).data });
  const launch = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      api<{ run_ids: string[] }>(`/v1/workflows/${workflow.id}/runs`, {
        method: 'POST',
        body: launchBody,
      }),
    onSuccess: async (result) => {
      toast.success(t('launch.queued', { count: result.data.run_ids.length }), {
        description: <a className="underline" href={(() => { const first = (fixedSelection ?? items.items).find(item => item.id === (launchBody.primary_item_id ?? itemIds[0])); return first?.container_id ? `/w/${workflow.workspace_id}/l/${first.container_id}?item=${first.id}&tab=workflows` : `/w/${workflow.workspace_id}/settings/runs?run=${result.data.run_ids[0]}`; })()}>{t('launch.viewStatus')}</a>,
      });
      await client.invalidateQueries({ queryKey: ['workflow-runs'] });
      onClose();
    },
  });
  const move = (index: number, by: number) =>
    setItemIds((current) => {
      const next = [...current];
      const [id] = next.splice(index, 1);
      next.splice(index + by, 0, id);
      return next;
    });
  return (
    <FormDialog
      open
      title={t('launch.title', { name: workflow.name })}
      submitLabel={t('launch.runOnce')}
      pending={launch.isPending}
      submitDisabled={check.isPending || check.isError || !check.data?.can_run || !manual || (itemMode === 'single' ? itemIds.length !== 1 : itemMode === 'multiple' ? itemIds.length < 1 || itemIds.length > 100 : itemIds.length !== 0)}
      onClose={onClose}
      onSubmit={() => { if (check.data?.can_run) launch.mutate(); }}
    >
      <p className="text-sm">{t('launch.automaticRuns', { summary: automaticSummary(workflow, [], types.data) })}</p>
      <p className="text-xs text-muted">{t('launch.author', { author: workflow.execution_author ?? t('launch.workflowAuthor') })}</p>
      <QueryFeedback query={check} />
      {check.data && <p role="status" className="text-sm">{check.data.reason}</p>}
      {launch.error && <Alert>{problemMessage(launch.error)}</Alert>}
      {fixedSelection && <p className="text-sm">{t('launch.selected', { count: fixedSelection.length, names: fixedSelection.map((item) => item.name).join(', ') })}</p>}
      {manual && itemMode !== 'none' && (
        <>
          {!fixedSelection && <>
          <Label id="launch-items-label">{itemMode === 'single' ? t('launch.item') : t('launch.items')}</Label>
          {!manual.collection_id && (
            <p className="text-xs text-muted">{t('launch.anyCollection')}</p>
          )}
          <Combobox
            aria-labelledby="launch-items-label"
            multiple={itemMode === 'multiple'}
            selected={itemIds.map((id) => options.find((option) => option.value === id) ?? { value: id, label: id })}
            options={options}
            onSearch={setQ}
            onChange={(values) => setItemIds(values.map((option) => option.value))}
          />
          <QueryFeedback query={items} />
          {items.hasNextPage && (
            <Button disabled={items.isFetchingNextPage} onClick={() => void items.fetchNextPage()}>
              {t('launch.moreItems')}
            </Button>
          )}
          </>}
          {manual.selection === 'selection' && (
            <>
              <Label htmlFor="launch-primary">{t('launch.primary', { label: hints?.item_label ?? t('launch.itemWord') })}</Label>
              <Select
                id="launch-primary"
                value={itemIds.includes(primary) ? primary : (itemIds[0] ?? '')}
                onChange={(event) => setPrimary(event.target.value)}
              >
                {itemIds.map((id) => (
                  <option key={id} value={id}>
                    {options.find((option) => option.value === id)?.label ?? id}
                  </option>
                ))}
              </Select>
              {hints?.primary_description && <p className="text-xs text-muted">{hints.primary_description}</p>}
              <Label id="launch-order-label">{hints?.order_label ?? t('launch.order')}</Label>
              <ol aria-labelledby="launch-order-label">
                {itemIds.map((id, index) => (
                  <li key={id} className="flex items-center gap-2 py-1">
                    {hints?.preview === 'image' && <ItemImage itemId={id} />}
                    <span className="flex-1">
                      {hints?.item_label && (
                        <span className="text-xs text-muted">
                          {hints.item_label} {index + 1}:{' '}
                        </span>
                      )}
                      {options.find((option) => option.value === id)?.label ?? id}
                    </span>
                    <Button disabled={index === 0} onClick={() => move(index, -1)}>
                      {t('launch.up')}
                    </Button>
                    <Button disabled={index === itemIds.length - 1} onClick={() => move(index, 1)}>
                      {t('launch.down')}
                    </Button>
                  </li>
                ))}
              </ol>
            </>
          )}
        </>
      )}
      <SchemaFields schema={schema} data={inputs} onChange={setInputs} workspaceId={workflow.workspace_id} />
    </FormDialog>
  );
}

/** A small preview of an item's current image file. */
function ItemImage({ itemId }: { itemId: string }) {
  const { t } = useTranslation('workflows');
  return <FilePreview itemId={itemId} alt={t('launch.previewAlt')} className="size-12 min-h-0 shrink-0" />;
}

export function WorkflowVersions({
  workflow,
  canManage,
  onEdit,
  onClose,
}: {
  workflow: Workflow;
  canManage: boolean;
  onEdit: (workflow: Workflow) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation('workflows');
  const format = useFormat();
  const versions = useQuery({
    queryKey: ['workflow-versions', workflow.id],
    queryFn: () =>
      allPages<{ number: number; source: string; definition: WorkflowDefinition; created_at: string; created_by: string }>(
        `/v1/workflows/${workflow.id}/versions`,
      ),
  });
  const [number, setNumber] = useState(workflow.current_version);
  const selected = versions.data?.find((version) => version.number === number);
  return (
    <FormDialog
      open
      title={t('versions.title', { name: workflow.name })}
      submitLabel={t('versions.edit')}
      submitDisabled={!canManage || !!workflow.builtin_key || !selected}
      onClose={onClose}
      onSubmit={() => {
        if (selected) onEdit({ ...workflow, source: selected.source, definition: selected.definition });
        onClose();
      }}
    >
      <QueryFeedback query={versions} />
      <Select aria-label={t('versions.label')} value={number} onChange={(event) => setNumber(Number(event.target.value))}>
        {versions.data?.map((version) => (
          <option key={version.number} value={version.number}>
            {t('versions.option', { number: version.number, date: format.dateTime(version.created_at), author: version.created_by })}
          </option>
        ))}
      </Select>
      <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-surface-muted p-3 text-xs">
        {selected?.source}
      </pre>
    </FormDialog>
  );
}

export function CopyBuiltIn({
  workspaceId,
  builtinKey,
  collectionId,
  parameters,
  onClose,
  onCopied,
}: {
  workspaceId: string;
  builtinKey: string;
  collectionId?: string;
  parameters: Record<string, unknown>;
  onClose: () => void;
  onCopied: () => Promise<void>;
}) {
  const { t } = useTranslation(['workflows', 'common']);
  const [name, setName] = useState('');
  const copy = useMutation({
    mutationFn: () =>
      api(`/v1/workspaces/${workspaceId}/workflow-builtins/${builtinKey}/copy`, {
        method: 'POST',
        body: { name: name.trim(), collection_id: collectionId, parameters },
      }),
    onSuccess: async () => {
      await onCopied();
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={t('copy.title')}
      description={t('copy.description')}
      submitLabel={t('copy.create')}
      pending={copy.isPending}
      onClose={onClose}
      onSubmit={() => copy.mutate()}
    >
      <Label htmlFor="workflow-copy-name">{t('common:fields.name')}</Label>
      <Input
        id="workflow-copy-name"
        required
        maxLength={100}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
    </FormDialog>
  );
}
