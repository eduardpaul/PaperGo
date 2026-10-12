import { useMutation } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { saveFile } from '@/api/files';
import { parseJSON, stringifyJSON } from '@/api/json';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';
import { SettingsSection } from '@/features/settings/section';

export function PackageTransfer({
  title,
  path,
  canImport,
  version,
  onImported,
}: {
  title: string;
  path: string;
  canImport: boolean;
  version?: number;
  onImported: () => Promise<void>;
}) {
  const [file, setFile] = useState<File>();
  const download = useMutation({
    mutationFn: async () => {
      const data = (await api<unknown>(`${path}/export`)).data;
      saveFile(
        new Blob([stringifyJSON(data)!], { type: 'application/json' }),
        `${title.toLowerCase().replaceAll(' ', '-')}.json`,
      );
    },
  });
  const upload = useMutation({
    mutationFn: async () =>
      api(`${path}/import`, { method: 'POST', ifMatch: version, body: parseJSON(await file!.text()) }),
    onSuccess: async () => {
      await onImported();
      toast.success(`${title} imported`);
      setFile(undefined);
    },
  });
  return (
    <SettingsSection title={`${title} package`} description="Export a package or import one to add its definitions.">
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={download.isPending} onClick={() => download.mutate()}>
          Export {title.toLowerCase()}
        </Button>
        {canImport && (
          <>
            <Input
              key={file?.name ?? 'empty'}
              type="file"
              accept=".json,application/json"
              aria-label={`${title} package file`}
              onChange={(event) => setFile(event.target.files?.[0])}
              className="max-w-xs"
            />
            <Button disabled={!file || upload.isPending} onClick={() => upload.mutate()}>
              Import {title.toLowerCase()}
            </Button>
          </>
        )}
      </div>
      {(download.error || upload.error) && (
        <Alert className="mt-3">{problemMessage(download.error ?? upload.error)}</Alert>
      )}
    </SettingsSection>
  );
}
