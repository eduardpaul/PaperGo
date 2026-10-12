import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { stringifyJSON } from '@/api/json';
import { allPages, useApiPages } from '@/api/pages';
import type { Resource, Workflow } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useFormat } from '@/lib/preferences';
import { formDefaults, normalizeForm } from './model';
import type { WorkflowDefinition } from './model';
import { SchemaFields } from './schema-form';

export function LaunchWorkflow({
  workflow,
  onClose,
  itemIds: initialItems = [],
}: {
  workflow: Workflow;
  onClose: () => void;
  itemIds?: string[];
}) {
  const client = useQueryClient();
  const definition = workflow.definition as WorkflowDefinition;
  const manual = definition.triggers.find((trigger) => trigger.type === 'manual');
  const schema = definition.input_schema ?? { type: 'object', properties: {} };
  const [inputs, setInputs] = useState<Record<string, unknown>>(formDefaults(schema));
  const [itemIds, setItemIds] = useState(initialItems);
  const [primary, setPrimary] = useState(initialItems[0] ?? '');
  const [q, setQ] = useState('');
  const items = useApiPages<Resource>(
    ['resources', workflow.workspace_id, manual?.collection_id, 'launch-items', q],
    manual?.collection_id ? `/v1/resources/${manual.collection_id}/query` : '/v1/resources',
    {
      enabled: !!manual,
      query: manual?.collection_id ? undefined : { workspace_id: workflow.workspace_id, q, surface: 'head' },
      body: manual?.collection_id ? { surface: 'head', query: { search: q || undefined } } : undefined,
    },
  );
  const options = items.items
    .filter((item) => item.kind === 'item')
    .map((item) => ({ value: item.id, label: item.name }));
  const launch = useMutation({
    mutationFn: () =>
      api<{ run_ids: string[] }>(`/v1/workflows/${workflow.id}/runs`, {
        method: 'POST',
        body: {
          item_ids: itemIds.length ? itemIds : undefined,
          primary_item_id:
            manual?.selection === 'selection' ? (itemIds.includes(primary) ? primary : itemIds[0]) : undefined,
          inputs: normalizeForm(schema, inputs),
        },
      }),
    onSuccess: async (result) => {
      toast.success(`${result.data.run_ids.length} workflow runs started`);
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
      title={`Run ${workflow.name}`}
      submitLabel="Run workflow"
      pending={launch.isPending}
      submitDisabled={!workflow.enabled || (!!manual?.collection_id && !itemIds.length) || itemIds.length > 100}
      onClose={onClose}
      onSubmit={() => launch.mutate()}
    >
      {manual && (
        <>
          <Label id="launch-items-label">{manual.collection_id ? 'Items' : 'Items (optional)'}</Label>
          {!manual.collection_id && (
            <p className="text-xs text-muted">
              Choose items to start one run per item. Starting without items requires manage access to this workspace.
            </p>
          )}
          <Combobox
            aria-labelledby="launch-items-label"
            multiple
            selected={itemIds.map((id) => options.find((option) => option.value === id) ?? { value: id, label: id })}
            options={options}
            onSearch={setQ}
            onChange={(values) => setItemIds(values.map((option) => option.value))}
          />
          <QueryFeedback query={items} />
          {items.hasNextPage && (
            <Button disabled={items.isFetchingNextPage} onClick={() => void items.fetchNextPage()}>
              More items
            </Button>
          )}
          {manual.selection === 'selection' && (
            <>
              <Label htmlFor="launch-primary">Primary item</Label>
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
              <ol>
                {itemIds.map((id, index) => (
                  <li key={id} className="flex items-center gap-2 py-1">
                    <span className="flex-1">{options.find((option) => option.value === id)?.label ?? id}</span>
                    <Button disabled={index === 0} onClick={() => move(index, -1)}>
                      Up
                    </Button>
                    <Button disabled={index === itemIds.length - 1} onClick={() => move(index, 1)}>
                      Down
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
  const format = useFormat();
  const versions = useQuery({
    queryKey: ['workflow-versions', workflow.id],
    queryFn: () =>
      allPages<{ number: number; definition: WorkflowDefinition; created_at: string; created_by: string }>(
        `/v1/workflows/${workflow.id}/versions`,
      ),
  });
  const [number, setNumber] = useState(workflow.current_version);
  const selected = versions.data?.find((version) => version.number === number);
  return (
    <FormDialog
      open
      title={`Versions of ${workflow.name}`}
      submitLabel="Use this definition"
      submitDisabled={!canManage || !!workflow.builtin_key || !selected}
      onClose={onClose}
      onSubmit={() => {
        if (selected) onEdit({ ...workflow, definition: selected.definition });
        onClose();
      }}
    >
      <QueryFeedback query={versions} />
      <Select aria-label="Workflow version" value={number} onChange={(event) => setNumber(Number(event.target.value))}>
        {versions.data?.map((version) => (
          <option key={version.number} value={version.number}>
            Version {version.number} · {format.dateTime(version.created_at)} · {version.created_by}
          </option>
        ))}
      </Select>
      <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-surface-muted p-3 text-xs">
        {selected && stringifyJSON(selected.definition)}
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
      title="Copy and customize built-in"
      description="The copy becomes an editable workflow. The built-in is turned off for this collection."
      submitLabel="Copy workflow"
      pending={copy.isPending}
      onClose={onClose}
      onSubmit={() => copy.mutate()}
    >
      <Label htmlFor="workflow-copy-name">Name</Label>
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
