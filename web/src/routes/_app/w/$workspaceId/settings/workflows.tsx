import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { builtIns, collectionChildren, workflows } from '@/api/queries';
import { useAccess } from '@/features/lists/access';
import type { BuiltInState, Workflow } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { SettingsSection } from '@/features/settings/section';
import { ConfirmDialog } from '@/features/settings/section';
import { QueryFeedback } from '@/features/admin/common';
import { WorkflowEditor } from '@/features/workflows/workflow-editor';
import { CopyBuiltIn, LaunchWorkflow, WorkflowVersions } from '@/features/workflows/workflow-tools';
import type { WorkflowDefinition } from '@/features/workflows/model';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/workflows')({ component: WorkflowsPage });

function WorkflowsPage() {
  const { workspaceId } = Route.useParams();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['workflows', workspaceId], queryFn: () => workflows(workspaceId) });
  const catalog = useQuery({ queryKey: ['workflow-builtins', workspaceId], queryFn: () => builtIns(workspaceId) });
  const { canManage } = useAccess(workspaceId);
  const [editing, setEditing] = useState<Workflow | 'new'>();
  const [launching, setLaunching] = useState<Workflow>();
  const [history, setHistory] = useState<Workflow>();
  const [deleting, setDeleting] = useState<Workflow>();
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['workflows', workspaceId] });
    await queryClient.invalidateQueries({ queryKey: ['workflow-builtins', workspaceId] });
  };
  const custom = (list.data ?? []).filter((workflow) => !workflow.builtin_key);
  const remove = useMutation({
    mutationFn: (workflow: Workflow) =>
      api(`/v1/workflows/${workflow.id}`, { method: 'DELETE', ifMatch: workflow.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await refresh();
    },
  });
  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title="Built-in workflows"
        description="Turn a built-in on for a collection. Parameters fill the shipped definition."
      >
        <QueryFeedback query={catalog} />
        <div className="flex flex-col gap-4">
          {(catalog.data ?? []).map((builtin) => (
            <BuiltInForm
              key={builtin.key}
              canManage={canManage}
              workspaceId={workspaceId}
              builtin={builtin}
              onChanged={refresh}
            />
          ))}
          {catalog.isFetched && (catalog.data?.length ?? 0) === 0 && (
            <p className="text-sm text-muted">No built-ins are available.</p>
          )}
        </div>
      </SettingsSection>
      <SettingsSection
        title="Workflows"
        description="Triggers, conditions, connected activity steps and manual launch forms."
        actions={
          canManage && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              New workflow
            </Button>
          )
        }
      >
        <QueryFeedback query={list} />
        <ul className="flex flex-col gap-2">
          {custom.map((workflow) => (
            <WorkflowRow
              key={workflow.id}
              workflow={workflow}
              canManage={canManage}
              onChanged={refresh}
              onEdit={() => setEditing(workflow)}
              onLaunch={() => setLaunching(workflow)}
              onHistory={() => setHistory(workflow)}
              onDelete={() => setDeleting(workflow)}
            />
          ))}
          {list.isFetched && custom.length === 0 && <li className="text-sm text-muted">No custom workflows.</li>}
        </ul>
      </SettingsSection>
      {editing && (
        <WorkflowEditor
          workspaceId={workspaceId}
          workflow={editing === 'new' ? undefined : editing}
          canManage={canManage}
          onClose={() => setEditing(undefined)}
        />
      )}
      {launching && <LaunchWorkflow workflow={launching} onClose={() => setLaunching(undefined)} />}
      {history && (
        <WorkflowVersions
          workflow={history}
          canManage={canManage}
          onEdit={setEditing}
          onClose={() => setHistory(undefined)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={`Delete ${deleting?.name ?? ''}?`}
        description="This stops new runs. Existing runs and version history remain."
        confirm="Delete workflow"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}

function WorkflowRow({
  workflow,
  canManage,
  onChanged,
  onEdit,
  onLaunch,
  onHistory,
  onDelete,
}: {
  workflow: Workflow;
  canManage: boolean;
  onChanged: () => Promise<void>;
  onEdit: () => void;
  onLaunch: () => void;
  onHistory: () => void;
  onDelete: () => void;
}) {
  const toggle = useMutation({
    mutationFn: () =>
      api(`/v1/workflows/${workflow.id}`, {
        method: 'PUT',
        ifMatch: workflow.version,
        body: {
          name: workflow.name,
          description: workflow.description,
          enabled: !workflow.enabled,
          definition: workflow.definition,
        },
      }),
    onSuccess: async () => {
      toast.success(workflow.enabled ? 'Workflow disabled' : 'Workflow enabled');
      await onChanged();
    },
  });
  return (
    <li className="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2">
      <div className="min-w-0 flex-1">
        <p className="truncate text-[13px] font-medium">{workflow.name}</p>
        <p className="truncate text-xs text-muted">{workflow.description}</p>
      </div>
      <Button size="sm" disabled={toggle.isPending || !canManage} onClick={() => toggle.mutate()}>
        {workflow.enabled ? 'Disable' : 'Enable'}
      </Button>
      <Button size="sm" onClick={onEdit}>
        {canManage ? 'Edit' : 'View'}
      </Button>
      <Button size="sm" onClick={onHistory}>
        Versions
      </Button>
      {(workflow.definition as WorkflowDefinition).triggers?.some((trigger) => trigger.type === 'manual') && (
        <Button size="sm" disabled={!workflow.enabled} onClick={onLaunch}>
          Run
        </Button>
      )}
      {canManage && (
        <Button size="sm" variant="danger" onClick={onDelete}>
          Delete
        </Button>
      )}
    </li>
  );
}

function BuiltInForm({
  workspaceId,
  builtin,
  onChanged,
  canManage,
}: {
  canManage: boolean;
  workspaceId: string;
  builtin: BuiltInState;
  onChanged: () => Promise<void>;
}) {
  const collections = useQuery({
    queryKey: ['resources', workspaceId, 'collections'],
    queryFn: () => collectionChildren(workspaceId),
  });
  const [collectionId, setCollectionId] = useState(builtin.workflows?.[0]?.collection_id ?? '');
  const existing = builtin.workflows?.find(
    (workflow) => (workflow.collection_id ?? '') === (builtin.per_collection ? collectionId : ''),
  );
  return (
    <div className="rounded-md border p-3">
      <p className="text-sm font-medium">{builtin.name}</p>
      <p className="mt-0.5 text-xs text-muted">{builtin.description}</p>
      {builtin.per_collection && (
        <Select
          className="mt-2"
          value={collectionId}
          onChange={(event) => setCollectionId(event.target.value)}
          aria-label="Collection"
        >
          <option value="">Collection…</option>
          {collections.data?.map((collection) => (
            <option key={collection.id} value={collection.id}>
              {collection.name}
            </option>
          ))}
        </Select>
      )}
      <BuiltInParameters
        key={`${workspaceId}:${builtin.key}:${collectionId}:${existing?.version ?? 0}`}
        workspaceId={workspaceId}
        builtin={builtin}
        collectionId={collectionId}
        existing={existing}
        canManage={canManage}
        onChanged={onChanged}
      />
      {(builtin.workflows ?? []).length > 1 && (
        <ul className="mt-2 text-xs text-muted">
          {builtin.workflows?.map((workflow) => (
            <li key={workflow.id}>
              {workflow.enabled ? 'On' : 'Off'}
              {workflow.collection_id ? ` · ${workflow.collection_id}` : ''}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function BuiltInParameters({
  workspaceId,
  builtin,
  collectionId,
  existing,
  onChanged,
  canManage,
}: {
  canManage: boolean;
  workspaceId: string;
  builtin: BuiltInState;
  collectionId: string;
  existing?: Workflow;
  onChanged: () => Promise<void>;
}) {
  const properties = builtin.parameters_schema?.properties ?? {};
  const [copying, setCopying] = useState(false);
  const [history, setHistory] = useState(false);
  const [parameters, setParameters] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {};
    for (const [key, spec] of Object.entries(properties)) {
      const current = existing?.parameters?.[key] ?? spec.default ?? '';
      initial[key] = current == null ? '' : String(current);
    }
    return initial;
  });
  const save = useMutation({
    mutationFn: (enabled: boolean) =>
      api(`/v1/workspaces/${workspaceId}/workflow-builtins/${builtin.key}`, {
        method: 'PUT',
        ifMatch: existing?.version,
        body: {
          collection_id: builtin.per_collection ? collectionId : undefined,
          enabled,
          parameters,
        },
      }),
    onSuccess: async () => {
      toast.success('Built-in updated');
      await onChanged();
    },
  });
  return (
    <>
      <div className="mt-2 grid gap-2">
        {Object.entries(properties).map(([key, spec]) => (
          <label key={key} className="grid gap-1 text-[13px]">
            <span>{spec.title || key}</span>
            <Input
              disabled={!canManage}
              value={parameters[key] ?? ''}
              onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.value }))}
            />
          </label>
        ))}
      </div>
      <div className="mt-3 flex gap-2">
        <Button
          size="sm"
          variant="primary"
          disabled={!canManage || save.isPending || (builtin.per_collection && !collectionId)}
          onClick={() => save.mutate(true)}
        >
          Enable
        </Button>
        <Button size="sm" disabled={!canManage || save.isPending || !existing} onClick={() => save.mutate(false)}>
          Disable
        </Button>
        <Button
          size="sm"
          disabled={!canManage || (builtin.per_collection && !collectionId)}
          onClick={() => setCopying(true)}
        >
          Copy and customize
        </Button>
        {existing && (
          <Button size="sm" onClick={() => setHistory(true)}>
            Versions
          </Button>
        )}
      </div>
      {copying && (
        <CopyBuiltIn
          workspaceId={workspaceId}
          builtinKey={builtin.key}
          collectionId={builtin.per_collection ? collectionId : undefined}
          parameters={parameters}
          onClose={() => setCopying(false)}
          onCopied={onChanged}
        />
      )}
      {history && existing && (
        <WorkflowVersions workflow={existing} canManage={false} onEdit={() => {}} onClose={() => setHistory(false)} />
      )}
    </>
  );
}
