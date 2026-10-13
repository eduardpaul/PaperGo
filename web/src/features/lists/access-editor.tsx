import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Trash2, UserPlus } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { peopleQuery } from '@/api/queries';
import type { PermissionGrant, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Combobox, type ComboboxOption } from '@/components/ui/combobox';
import { Alert } from '@/components/ui/feedback';
import { Checkbox, Select } from '@/components/ui/select';
import { useHasScope } from '@/features/admin/common';
import { groupsQuery } from '@/features/admin/principals';
import { usePeopleNames } from '@/features/fields/people';
import { problemMessage } from '@/lib/errors';
import { permissionsQuery } from './access';

export const accessLevels = [
  { value: 'read', label: 'Read', hint: 'See published content' },
  { value: 'read_draft', label: 'Read drafts', hint: 'Also see unpublished changes' },
  { value: 'write', label: 'Edit', hint: 'Add, change and delete content' },
  { value: 'publish', label: 'Publish', hint: 'Edit and publish' },
  { value: 'manage', label: 'Manage', hint: 'Everything, including settings and permissions' },
] as const;

export function accessLabel(action: string) {
  return accessLevels.find((level) => level.value === action)?.label ?? action;
}

const roleNames: Record<string, string> = {
  workspace_visitor: 'Workspace visitors',
  workspace_member: 'Workspace members',
  workspace_owner: 'Workspace owners',
};

const principalKey = (grant: Pick<PermissionGrant, 'principal_type' | 'principal_id'>) =>
  `${grant.principal_type}:${grant.principal_id}`;

/** Names and pickable options for people and groups, as far as the caller may read them. */
function usePrincipals() {
  const names = usePeopleNames();
  const people = useQuery(peopleQuery);
  const groups = useQuery({ ...groupsQuery, enabled: useHasScope('group.read') });
  const groupNames = new Map((groups.data ?? []).map((group) => [group.id, group.name]));
  const nameOf = (grant: Pick<PermissionGrant, 'principal_type' | 'principal_id'>) =>
    roleNames[grant.principal_type] ??
    (grant.principal_type === 'group'
      ? (groupNames.get(grant.principal_id) ?? grant.principal_id)
      : (names.get(grant.principal_id) ?? grant.principal_id));
  const options: ComboboxOption[] = [
    ...(people.data ?? []).map((subject) => ({
      value: `user:${subject}`,
      label: names.get(subject) ?? subject,
      hint: 'Person',
    })),
    ...(groups.data ?? []).map((group) => ({ value: `group:${group.id}`, label: group.name, hint: 'Group' })),
  ];
  return { nameOf, options };
}

/** Who can use a list, library, folder or item: inherited from its parent, or its own grants. */
export function AccessEditor({ resource }: { resource: Resource }) {
  const queryClient = useQueryClient();
  const current = useQuery(permissionsQuery(resource.id));
  if (current.isPending) return <p className="text-sm text-muted">Loading…</p>;
  if (!current.data)
    return <p className="text-sm text-muted">Only people who manage this {resource.kind} can see and change its permissions.</p>;
  return (
    <AccessForm
      key={`${resource.version}:${current.data.inherit}`}
      resource={resource}
      inherit={current.data.inherit}
      grants={current.data.grants ?? []}
      effective={current.data.effective_grants ?? []}
      onSaved={() => queryClient.invalidateQueries({ queryKey: ['permissions', resource.id] })}
    />
  );
}

function AccessForm({
  resource,
  inherit,
  grants,
  effective,
  onSaved,
}: {
  resource: Resource;
  inherit: boolean;
  grants: PermissionGrant[];
  effective: PermissionGrant[];
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const { nameOf, options } = usePrincipals();
  const [localInherit, setInherit] = useState(inherit);
  const [localGrants, setGrants] = useState<PermissionGrant[]>(grants.map((grant) => ({ ...grant, effect: 'allow' })));
  const [copyInherited, setCopy] = useState(true);
  const [adding, setAdding] = useState<ComboboxOption[]>([]);
  const [level, setLevel] = useState('read');
  const save = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      api(`/v1/resources/${resource.id}/permissions`, {
        method: 'PUT',
        ifMatch: resource.version,
        body: {
          inherit: localInherit,
          copy_inherited: inherit && !localInherit && copyInherited,
          grants: localInherit ? [] : localGrants,
        },
      }),
    onSuccess: async () => {
      toast.success('Permissions saved');
      await queryClient.invalidateQueries({ queryKey: ['resources', resource.id] });
      onSaved();
    },
  });
  const breaking = inherit && !localInherit;
  const shown = localInherit || breaking ? effective : localGrants;
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      {save.isError && <Alert>{problemMessage(save.error)}</Alert>}
      <fieldset className="flex flex-col gap-2 text-[13px]">
        <label className="flex items-start gap-2">
          <input type="radio" name="inherit" className="mt-0.5" checked={localInherit} onChange={() => setInherit(true)} />
          <span>
            <span className="font-medium">Same as the parent</span>
            <span className="block text-xs text-muted">Changes to the parent’s permissions apply here too.</span>
          </span>
        </label>
        <label className="flex items-start gap-2">
          <input type="radio" name="inherit" className="mt-0.5" checked={!localInherit} onChange={() => setInherit(false)} />
          <span>
            <span className="font-medium">Its own permissions</span>
            <span className="block text-xs text-muted">Only the people and groups listed below have access.</span>
          </span>
        </label>
      </fieldset>
      {breaking && (
        <label className="flex items-center gap-2 text-[13px]">
          <Checkbox checked={copyInherited} onChange={(event) => setCopy(event.target.checked)} />
          Start with the parent’s permissions
        </label>
      )}
      <ul className="flex flex-col divide-y rounded-md border">
        {shown.map((grant, index) => (
          <li key={`${principalKey(grant)}-${grant.action}-${index}`} className="flex items-center gap-2 px-3 py-2">
            <span className="min-w-0 flex-1 truncate text-[13px]">
              {nameOf(grant)}
              <span className="ml-1 text-xs text-muted">{grant.principal_type === 'group' ? 'Group' : ''}</span>
            </span>
            {localInherit || breaking ? (
              <span className="text-xs text-muted">{accessLabel(grant.action)}</span>
            ) : (
              <>
                <Select
                  aria-label={`Access of ${nameOf(grant)}`}
                  className="w-36"
                  value={grant.action}
                  onChange={(event) =>
                    setGrants((current) =>
                      current.map((entry, i) => (i === index ? { ...entry, action: event.target.value } : entry)),
                    )
                  }
                >
                  {accessLevels.map((entry) => (
                    <option key={entry.value} value={entry.value}>
                      {entry.label}
                    </option>
                  ))}
                </Select>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`Remove ${nameOf(grant)}`}
                  onClick={() => setGrants((current) => current.filter((_, i) => i !== index))}
                >
                  <Trash2 />
                </Button>
              </>
            )}
          </li>
        ))}
        {shown.length === 0 && <li className="px-3 py-2 text-[13px] text-muted">Nobody has access yet.</li>}
      </ul>
      {!localInherit && !breaking && (
        <div className="flex flex-col gap-2 sm:flex-row">
          <div className="min-w-0 flex-1">
            <Combobox
              selected={adding}
              options={options.filter(
                (option) => !localGrants.some((grant) => principalKey(grant) === option.value),
              )}
              placeholder="Add a person or group…"
              onChange={setAdding}
            />
          </div>
          <Select aria-label="Access level" className="sm:w-36" value={level} onChange={(event) => setLevel(event.target.value)}>
            {accessLevels.map((entry) => (
              <option key={entry.value} value={entry.value}>
                {entry.label}
              </option>
            ))}
          </Select>
          <Button
            disabled={!adding.length}
            onClick={() => {
              const [type, ...id] = adding[0].value.split(':');
              setGrants((current) => [
                ...current,
                { principal_type: type as PermissionGrant['principal_type'], principal_id: id.join(':'), action: level, effect: 'allow' },
              ]);
              setAdding([]);
            }}
          >
            <UserPlus /> Add
          </Button>
        </div>
      )}
      <p className="text-xs text-muted">
        {accessLevels.map((entry) => `${entry.label}: ${entry.hint.toLowerCase()}`).join(' · ')}
      </p>
      <div className="flex justify-end">
        <Button type="submit" variant="primary" disabled={save.isPending || (localInherit === inherit && localInherit)}>
          Save permissions
        </Button>
      </div>
    </form>
  );
}
