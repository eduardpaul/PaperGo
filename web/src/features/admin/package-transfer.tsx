import { useMutation } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { saveFile } from '@/api/files';
import { parseJSON, stringifyJSON } from '@/api/json';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { SettingsSection } from '@/features/settings/section';

// The packages, with the name their export is saved under in every language.
const packages = { taxonomy: 'taxonomy', smartFolders: 'smart-folders' } as const;

export function PackageTransfer({
  kind,
  path,
  canImport,
  version,
  onImported,
}: {
  kind: keyof typeof packages;
  path: string;
  canImport: boolean;
  version?: number;
  onImported: () => Promise<void>;
}) {
  const { t } = useTranslation('admin');
  const [file, setFile] = useState<File>();
  const download = useMutation({
    mutationFn: async () => {
      const data = (await api<unknown>(`${path}/export`)).data;
      saveFile(
        new Blob([stringifyJSON(data)!], { type: 'application/json' }),
        `${packages[kind]}.json`,
      );
    },
  });
  const upload = useMutation({
    mutationFn: async () =>
      api(`${path}/import`, { method: 'POST', ifMatch: version, body: parseJSON(await file!.text()) }),
    onSuccess: async () => {
      await onImported();
      toast.success(t(`package.${kind}.imported`));
      setFile(undefined);
    },
  });
  return (
    <SettingsSection title={t(`package.${kind}.title`)} description={t('package.description')}>
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={download.isPending} onClick={() => download.mutate()}>
          {t(`package.${kind}.export`)}
        </Button>
        {canImport && (
          <>
            <Input
              key={file?.name ?? 'empty'}
              type="file"
              accept=".json,application/json"
              aria-label={t(`package.${kind}.file`)}
              onChange={(event) => setFile(event.target.files?.[0])}
              className="max-w-xs"
            />
            <Button disabled={!file || upload.isPending} onClick={() => upload.mutate()}>
              {t(`package.${kind}.import`)}
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
