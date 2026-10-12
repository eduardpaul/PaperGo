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

export const Route = createFileRoute('/_app/admin/roles')({ component: RolesPage });
function RolesPage() {
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
  if (!canRead) return <Alert>You need role.read to view roles.</Alert>;
  return (
    <>
      <SettingsSection
        title="Roles"
        description="What people may do across the organization. Workspace and collection access is managed separately."
        actions={
          canEdit && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> New role
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
                      {role.scopes.length} scopes · {role.description}
                    </span>
                  </span>
                </button>
                {role.builtin && <Badge>Built in</Badge>}
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`Inspect ${role.name}`}
                  onClick={() => setEditing(role)}
                >
                  <Pencil />
                </Button>
                {canEdit && !role.builtin && (
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={`Delete ${role.name}`}
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
        title={`Delete ${deleting?.name ?? ''}?`}
        description="Everyone assigned this role loses its scopes."
        confirm="Delete role"
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
      title={role ? `Role ${role.name}` : 'New role'}
      submitLabel="Save role"
      submitDisabled={readOnly || !name.trim()}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => !readOnly && save.mutate()}
    >
      {role?.builtin && (
        <p className="text-xs text-muted">
          Built-in role scopes and metadata are read-only. Assign people and groups from the role list.
        </p>
      )}
      <fieldset disabled={readOnly} className="flex flex-col gap-3">
        <Field id="role-name" label="Name">
          <Input id="role-name" required value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        <Field id="role-description" label="Description">
          <Textarea
            id="role-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        <QueryFeedback query={catalog} />
        <fieldset>
          <legend className="mb-2 font-medium">Scopes</legend>
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
