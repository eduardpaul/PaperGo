import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Alert } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';

export function RenameDialog({ item, onClose }: { item: Resource; onClose: () => void }) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const [name, setName] = useState(item.name);
  const rename = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/resources/${item.id}`, { method: 'PATCH', ifMatch: item.version, body: { name: name.trim() } }),
    onSuccess: async () => { await client.invalidateQueries({ queryKey: keys.resources }); toast.success(t('rename.done')); onClose(); },
  });
  return <FormDialog open title={t('common:actions.rename')} submitLabel={t('common:actions.rename')} pending={rename.isPending} submitDisabled={!name.trim() || name.trim() === item.name} onClose={onClose} onSubmit={() => rename.mutate()}>
    {rename.error && <Alert>{problemMessage(rename.error)}</Alert>}
    <Label htmlFor="rename-item">{t('common:fields.name')}</Label><Input id="rename-item" value={name} maxLength={1024} onChange={(event) => setName(event.target.value)} autoFocus />
    <p className="text-xs text-muted">{t('rename.hint')}</p>
  </FormDialog>;
}
