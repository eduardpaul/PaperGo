import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { builtIns } from '@/api/queries';
import type { Workflow } from '@/api/types';
import type { WorkflowDefinition } from '@/features/workflows/model';
import { Button } from '@/components/ui/button';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { SettingRow, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';
import { useSearchAvailable } from '@/lib/search';

/** The built-in that fills the search.index role of each list and library (docs/workflows.md#roles). */
const searchBuiltIn = 'search.index';

/** What fills a role in a collection: its automatic provider, if any. */
interface RoleState {
  role?: { key: string };
  automatic_enabled: boolean;
  workflow?: Workflow;
}

/**
 * Whether a list's or library's items are kept in search. Off, searching
 * inside it matches names only: lists of readings or measurements seldom
 * need more, and every change then skips the index. It switches the
 * collection's search.index built-in, keeping the rest of its settings. A
 * copy of the built-in that fills the role instead is named, not switched:
 * turning the built-in on would turn the copy off.
 */
export function CollectionSearchSettings({ workspaceId, collectionId, canManage }: { workspaceId: string; collectionId: string; canManage: boolean }) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const available = useSearchAvailable();
  const catalog = useQuery({ queryKey: ['workflow-builtins', workspaceId], queryFn: () => builtIns(workspaceId), enabled: available });
  const builtin = catalog.data?.find((entry) => entry.key === searchBuiltIn);
  const roles = useQuery({
    queryKey: ['workflow-roles', collectionId],
    queryFn: async () => (await api<{ value: RoleState[] }>(`/v1/resources/${collectionId}/workflow-roles`)).data.value,
    enabled: available,
  });
  const provider = roles.data?.find((state) => state.role?.key === searchBuiltIn);
  const custom = provider?.automatic_enabled && provider.workflow?.builtin_key !== searchBuiltIn ? provider.workflow : undefined;
  const existing = builtin?.workflows?.find((entry) => entry.collection_id === collectionId);
  // A collection without its own configuration indexes by default.
  const enabled = custom !== undefined || (existing ? existing.automatic_enabled && existing.state === 'published' : builtin?.default !== false);
  const [choice, setChoice] = useState<'on' | 'off'>();
  const current = choice ?? (enabled ? 'on' : 'off');
  const save = useMutation({
    mutationFn: () => {
      const triggers = (existing?.definition as WorkflowDefinition | undefined)?.triggers ?? [];
      return api(`/v1/workspaces/${workspaceId}/workflow-builtins/${searchBuiltIn}`, {
        method: 'PUT',
        ifMatch: existing?.version,
        body: {
          collection_id: collectionId,
          automatic_enabled: current === 'on',
          manual_enabled: existing?.manual_enabled ?? true,
          // Indexing needs a published workflow; turning it off changes nothing else.
          state: current === 'on' ? 'published' : (existing?.state ?? 'published'),
          disabled_triggers: triggers.filter((trigger) => trigger.disabled).map((trigger) => trigger.type),
          parameters: existing?.parameters ?? {},
        },
      });
    },
    onSuccess: async () => {
      setChoice(undefined);
      toast.success(t('lists:search.saved'));
      await client.invalidateQueries({ queryKey: ['workflow-builtins', workspaceId] });
      await client.invalidateQueries({ queryKey: ['workflows', workspaceId] });
      await client.invalidateQueries({ queryKey: ['workflow-roles', collectionId] });
    },
  });
  if (!available || !builtin || roles.isPending) return null;
  return (
    <SettingsSection
      title={t('lists:search.title')}
      description={t('lists:search.description')}
      actions={
        canManage &&
        !custom && (
          <Button variant="primary" disabled={save.isPending || current === (enabled ? 'on' : 'off')} onClick={() => save.mutate()}>
            {t('common:actions.save')}
          </Button>
        )
      }
    >
      <SettingRow id={`search-${collectionId}`} label={t('lists:search.label')} hint={custom ? t('lists:search.customHint', { name: custom.name }) : current === 'on' ? t('lists:search.onHint') : t('lists:search.offHint')}>
        <Select id={`search-${collectionId}`} value={current} disabled={!canManage || custom !== undefined} onChange={(event) => setChoice(event.target.value as 'on' | 'off')}>
          <option value="on">{t('lists:search.on')}</option>
          <option value="off">{t('lists:search.off')}</option>
        </Select>
      </SettingRow>
      <QueryFeedback query={save} />
    </SettingsSection>
  );
}
