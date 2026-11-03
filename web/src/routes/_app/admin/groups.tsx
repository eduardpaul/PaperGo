import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { ChevronRight, Pencil, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Textarea } from '@/components/ui/input';
import { Field, QueryFeedback, useHasScope } from '@/features/admin/common';
import { GroupInbox } from '@/features/admin/group-inbox';
import { groupsQuery, Principals } from '@/features/admin/principals';
import type { IdentityGroup } from '@/features/admin/principals';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/admin/groups')({ component: GroupsPage });
function GroupsPage() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const canRead = useHasScope('group.read');
  const canEdit = useHasScope('group.manage');
  const groups = useQuery({ ...groupsQuery, enabled: canRead });
  const [editing, setEditing] = useState<IdentityGroup | 'new'>();
  const [deleting, setDeleting] = useState<IdentityGroup>();
  const [open, setOpen] = useState('');
  const remove = useMutation({
    mutationFn: (group: IdentityGroup) =>
      api(`/v1/identity/groups/${group.id}`, { method: 'DELETE', ifMatch: group.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: groupsQuery.queryKey });
    },
  });
  if (!canRead) return <Alert>{t('groups.needRead')}</Alert>;
  return (
    <>
      <SettingsSection
        title={t('groups.title')}
        description={t('groups.description')}
        actions={
          canEdit && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> {t('groups.new')}
            </Button>
          )
        }
      >
        <QueryFeedback query={groups} />
        <ul className="divide-y">
          {groups.data?.map((group) => (
            <li key={group.id}>
              <div className="flex items-center gap-2 py-3">
                <button
                  type="button"
                  className="flex flex-1 items-center gap-2 text-left"
                  aria-expanded={open === group.id}
                  onClick={() => setOpen(open === group.id ? '' : group.id)}
                >
                  <ChevronRight className="size-4" />
                  <span>
                    <span className="font-medium">{group.name}</span>
                    <span className="block text-xs text-muted">{group.description}</span>
                  </span>
                </button>
                {group.source === 'oidc' && <Badge>{t('groups.identityProvider')}</Badge>}
                {canEdit && (
                  <>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={t('groups.edit', { name: group.name })}
                      onClick={() => setEditing(group)}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={t('groups.delete', { name: group.name })}
                      onClick={() => setDeleting(group)}
                    >
                      <Trash2 />
                    </Button>
                  </>
                )}
              </div>
              {open === group.id && (
                <>
                  {group.source === 'oidc' && (
                    <p className="mb-2 text-xs text-muted">{t('groups.managedByProvider')}</p>
                  )}
                  <Principals
                    path={`/v1/identity/groups/${group.id}/members`}
                    canEdit={canEdit && group.source !== 'oidc'}
                    excludeGroup={group.id}
                  />
                  <GroupInbox groupId={group.id} />
                </>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      {editing && <GroupDialog group={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(next) => !next && setDeleting(undefined)}
        title={t('groups.deleteTitle', { name: deleting?.name ?? '' })}
        description={t('groups.deleteDescription')}
        confirm={t('groups.deleteConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}
function GroupDialog({ group, onClose }: { group?: IdentityGroup; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const [name, setName] = useState(group?.name ?? '');
  const [description, setDescription] = useState(group?.description ?? '');
  const save = useMutation({
    mutationFn: () =>
      api(group ? `/v1/identity/groups/${group.id}` : '/v1/identity/groups', {
        method: group ? 'PUT' : 'POST',
        ifMatch: group?.version,
        body: { name: name.trim(), description },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: groupsQuery.queryKey });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={group ? t('groups.edit', { name: group.name }) : t('groups.new')}
      submitLabel={t('groups.save')}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Field id="group-name" label={t('common:fields.name')}>
        <Input id="group-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field id="group-description" label={t('common:fields.description')}>
        <Textarea id="group-description" value={description} onChange={(event) => setDescription(event.target.value)} />
      </Field>
    </FormDialog>
  );
}
