import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { ChevronRight, Pencil, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys, scopeCatalog } from '@/api/queries';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Textarea } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/select';
import { Field, QueryFeedback, useHasScope } from '@/features/admin/common';
import { Principals, rolesQuery } from '@/features/admin/principals';
import type { IdentityRole } from '@/features/admin/principals';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/admin/roles')({ component: RolesPage });
function RolesPage() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const canRead = useHasScope('role.read');
  const canEdit = useHasScope('role.manage');
  const roles = useQuery({ ...rolesQuery, enabled: canRead });
  const [editing, setEditing] = useState<IdentityRole | 'new'>();
  const [deleting, setDeleting] = useState<IdentityRole>();
  const [open, setOpen] = useState('');
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: rolesQuery.queryKey }),
      client.invalidateQueries({ queryKey: keys.me }),
    ]);
  };
  const remove = useMutation({
    mutationFn: (role: IdentityRole) =>
      api(`/v1/identity/roles/${role.id}`, { method: 'DELETE', ifMatch: role.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await refresh();
    },
  });
  if (!canRead) return <Alert>{t('roles.needRead')}</Alert>;
  return (
    <>
      <SettingsSection
        title={t('roles.title')}
        description={t('roles.description')}
        actions={
          canEdit && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> {t('roles.new')}
            </Button>
          )
        }
      >
        <QueryFeedback query={roles} />
        <ul className="divide-y">
          {roles.data?.map((role) => (
            <li key={role.id}>
              <div className="flex items-center gap-2 py-3">
                <button
                  type="button"
                  className="flex flex-1 items-center gap-2 text-left"
                  aria-expanded={open === role.id}
                  onClick={() => setOpen(open === role.id ? '' : role.id)}
                >
                  <ChevronRight className="size-4" />
                  <span>
                    <span className="font-medium">{role.name}</span>
                    <span className="block text-xs text-muted">
                      {t('roles.summary', { count: role.scopes.length, description: role.description })}
                    </span>
                  </span>
                </button>
                {role.builtin && <Badge>{t('roles.builtIn')}</Badge>}
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={t('roles.inspect', { name: role.name })}
                  onClick={() => setEditing(role)}
                >
                  <Pencil />
                </Button>
                {canEdit && !role.builtin && (
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={t('roles.delete', { name: role.name })}
                    onClick={() => setDeleting(role)}
                  >
                    <Trash2 />
                  </Button>
                )}
              </div>
              {open === role.id && <Principals path={`/v1/identity/roles/${role.id}/assignments`} canEdit={canEdit} />}
            </li>
          ))}
        </ul>
      </SettingsSection>
      {editing && (
        <RoleDialog
          role={editing === 'new' ? undefined : editing}
          canEdit={canEdit}
          onClose={() => setEditing(undefined)}
          onSaved={refresh}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(next) => !next && setDeleting(undefined)}
        title={t('roles.deleteTitle', { name: deleting?.name ?? '' })}
        description={t('roles.deleteDescription')}
        confirm={t('roles.deleteConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}
function RoleDialog({
  role,
  canEdit,
  onClose,
  onSaved,
}: {
  role?: IdentityRole;
  canEdit: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useTranslation(['admin', 'common']);
  const catalog = useQuery({ queryKey: ['scope-catalog'], queryFn: scopeCatalog });
  const [name, setName] = useState(role?.name ?? '');
  const [description, setDescription] = useState(role?.description ?? '');
  const [scopes, setScopes] = useState(role?.scopes ?? []);
  const readOnly = !canEdit || !!role?.builtin;
  const save = useMutation({
    mutationFn: () =>
      api(role ? `/v1/identity/roles/${role.id}` : '/v1/identity/roles', {
        method: role ? 'PUT' : 'POST',
        ifMatch: role?.version,
        body: { name: name.trim(), description, scopes },
      }),
    onSuccess: async () => {
      await onSaved();
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={role ? t('roles.role', { name: role.name }) : t('roles.new')}
      submitLabel={t('roles.save')}
      submitDisabled={readOnly || !name.trim()}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => !readOnly && save.mutate()}
    >
      {role?.builtin && (
        <p className="text-xs text-muted">
          {t('roles.builtInHint')}
        </p>
      )}
      <fieldset disabled={readOnly} className="flex flex-col gap-3">
        <Field id="role-name" label={t('common:fields.name')}>
          <Input id="role-name" required value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        <Field id="role-description" label={t('common:fields.description')}>
          <Textarea
            id="role-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        <QueryFeedback query={catalog} />
        <fieldset>
          <legend className="mb-2 font-medium">{t('roles.scopes')}</legend>
          <ul className="grid gap-1">
            {catalog.data?.map((scope) => (
              <li key={scope.name}>
                <label className="flex items-center gap-2 text-xs">
                  <Checkbox
                    checked={scopes.includes(scope.name)}
                    onChange={(event) =>
                      setScopes((current) =>
                        event.target.checked ? [...current, scope.name] : current.filter((name) => name !== scope.name),
                      )
                    }
                  />
                  {scope.name}
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
      </fieldset>
    </FormDialog>
  );
}
