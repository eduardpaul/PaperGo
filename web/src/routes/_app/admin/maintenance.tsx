import { useMutation } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { useHasScope } from '@/features/admin/common';
import { OperationProgress } from '@/features/admin/operation-progress';
import type { Operation } from '@/features/admin/operation-progress';
import { SettingRow, SettingsSection } from '@/features/settings/section';

interface PortabilitySearch {
  export?: string;
  import?: string;
}
export const Route = createFileRoute('/_app/admin/maintenance')({
  validateSearch: (search: Record<string, unknown>): PortabilitySearch => ({
    export: typeof search.export === 'string' ? search.export : undefined,
    import: typeof search.import === 'string' ? search.import : undefined,
  }),
  component: PortabilityPage,
});
function PortabilityPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const canManage = useHasScope('organization.manage');
  const [file, setFile] = useState<File>();
  const create = useMutation({
    mutationFn: async () => (await api<Operation>('/v1/portability/exports', { method: 'POST' })).data,
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
          query: { dry_run: String(dryRun) },
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
      description="Everything with its files, as one package, for another installation or safekeeping."
    >
      <SettingRow id="new-export" label="New export">
        <Button id="new-export" disabled={create.isPending} onClick={() => create.mutate()}>
          Export organization
        </Button>
      </SettingRow>
      {search.export && <OperationProgress key={search.export} id={search.export} label="Export" />}
      <SettingRow
        id="package-file"
        label="Import a package"
        hint="Try it first to validate the package and report what it would create."
      >
        <Input
          id="package-file"
          type="file"
          accept=".zip,application/zip"
          onChange={(event) => setFile(event.target.files?.[0])}
        />
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
