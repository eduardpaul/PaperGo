import { useMutation, useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { workspacesQuery } from '@/api/queries';
import { Checkbox, Select } from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { useHasScope } from '@/features/admin/common';
import { OperationProgress } from '@/features/admin/operation-progress';
import type { Operation } from '@/features/admin/operation-progress';
import { SettingRow, SettingsSection } from '@/features/settings/section';

interface PortabilitySearch {
  workspace?: string;
  export?: string;
  import?: string;
}
export const Route = createFileRoute('/_app/admin/maintenance')({
  validateSearch: (search: Record<string, unknown>): PortabilitySearch => ({
    workspace: typeof search.workspace === 'string' ? search.workspace : undefined,
    export: typeof search.export === 'string' ? search.export : undefined,
    import: typeof search.import === 'string' ? search.import : undefined,
  }),
  component: PortabilityPage,
});
export function PortabilityPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const canManage = useHasScope('organization.manage');
  const [file, setFile] = useState<File>();
  const [includeData, setIncludeData] = useState(true);
  const [workspaceName, setWorkspaceName] = useState('');
  const workspaces = useQuery({ ...workspacesQuery, enabled: canManage });
  const create = useMutation({
    mutationFn: async () => (await api<Operation>('/v1/portability/exports', {
      method: 'POST', body: { workspace_id: search.workspace || undefined, include_data: includeData },
    })).data,
    onSuccess: (operation) => {
      void navigate({ search: (current) => ({ ...current, export: operation.id }) });
    },
  });
  const upload = useMutation({
    mutationFn: async (dryRun: boolean) =>
      (
        await api<Operation>('/v1/portability/imports', {
          method: 'POST',
          raw: file,
          headers: { 'Content-Type': 'application/zip' },
          query: { dry_run: String(dryRun), workspace_name: workspaceName.trim() || undefined },
        })
      ).data,
    onSuccess: (operation) => {
      void navigate({ search: (current) => ({ ...current, import: operation.id }) });
    },
  });
  if (!canManage) return <Alert>You need organization.manage to export or import the organization.</Alert>;
  return (
    <SettingsSection
      title="Export and import"
      description="Copy a workspace to another PaperGo instance, with its configuration and optional content. Organization packages transfer the whole instance."
    >
      <SettingRow id="export-scope" label="Export scope">
        <Select id="export-scope" value={search.workspace ?? ''} disabled={create.isPending} onChange={(event) => {
          void navigate({ search: (current) => ({ ...current, workspace: event.target.value || undefined }) });
        }}>
          <option value="">Whole organization</option>
          {workspaces.data?.filter((workspace) => !workspace.personal_owner_id).map((workspace) => (
            <option key={workspace.id} value={workspace.id}>{workspace.name}</option>
          ))}
        </Select>
      </SettingRow>
      <SettingRow id="include-data" label="Include content" hint="Include items, folders, history and all file versions. Clear this to copy configuration only.">
        <label htmlFor="include-data" className="flex items-center gap-2">
          <Checkbox id="include-data" checked={includeData} onChange={(event) => setIncludeData(event.target.checked)} />
          Include workspace data and files
        </label>
      </SettingRow>
      <SettingRow id="new-export" label={search.workspace ? 'Export workspace' : 'Export organization'}>
        <Button id="new-export" disabled={create.isPending} onClick={() => create.mutate()}>
          {search.workspace ? 'Export workspace' : 'Export organization'}
        </Button>
      </SettingRow>
      {search.export && <OperationProgress key={search.export} id={search.export} label="Export" />}
      <SettingRow
        id="package-file"
        label="Import a package"
        hint="Workspace packages create a new workspace alongside existing ones. Organization packages require an instance without resources. Try it first to validate without making changes."
      >
        <Input
          id="package-file"
          type="file"
          accept=".zip,application/zip"
          onChange={(event) => setFile(event.target.files?.[0])}
        />
        <label htmlFor="import-workspace-name" className="mt-3 block text-sm">New workspace name (optional, workspace packages only)</label>
        <Input id="import-workspace-name" value={workspaceName} onChange={(event) => setWorkspaceName(event.target.value)} placeholder="Use the name from the package" />
        <div className="mt-2 flex gap-2">
          <Button disabled={!file || upload.isPending} onClick={() => upload.mutate(true)}>
            Try it
          </Button>
          <Button variant="primary" disabled={!file || upload.isPending} onClick={() => upload.mutate(false)}>
            Import
          </Button>
        </div>
      </SettingRow>
      {search.import && <OperationProgress key={search.import} id={search.import} label="Import" />}
    </SettingsSection>
  );
}
