import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { UserProfile } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import type { ComboboxOption } from '@/components/ui/combobox';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { useTranslation } from '@/i18n';
import { QueryFeedback, useHasScope } from './common';

export interface Principal {
  principal_type: 'user' | 'group';
  principal_id: string;
}
export interface IdentityGroup {
  id: string;
  name: string;
  description: string;
  source: string;
  version: number;
}
export interface IdentityRole {
  id: string;
  name: string;
  description: string;
  scopes: string[];
  builtin: boolean;
  version: number;
}
export const groupsQuery = {
  queryKey: ['admin', 'groups'],
  queryFn: async () => (await api<IdentityGroup[]>('/v1/identity/groups')).data,
};
export const rolesQuery = {
  queryKey: ['admin', 'roles'],
  queryFn: async () => (await api<IdentityRole[]>('/v1/identity/roles')).data,
};

/** PaperDotNet's people/group picker, with PaperGo's principal-keyed membership endpoints. */
export function Principals({ path, canEdit, excludeGroup }: { path: string; canEdit: boolean; excludeGroup?: string }) {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const key = ['admin', 'principals', path];
  const list = useQuery({ queryKey: key, queryFn: async () => (await api<Principal[]>(path)).data });
  const userRead = useHasScope('user.read');
  const groupRead = useHasScope('group.read');
  const users = useQuery({
    queryKey: ['admin', 'users'],
    enabled: userRead,
    queryFn: async () => (await api<UserProfile[]>('/v1/identity/users')).data,
  });
  const groups = useQuery({ ...groupsQuery, enabled: groupRead });
  const [selected, setSelected] = useState<ComboboxOption[]>([]);
  const [type, setType] = useState<Principal['principal_type']>('user');
  const [id, setId] = useState('');
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: key }),
      client.invalidateQueries({ queryKey: keys.me }),
      client.invalidateQueries({ queryKey: keys.people }),
      client.invalidateQueries({ queryKey: keys.resources }),
      client.invalidateQueries({ queryKey: keys.workspaces }),
    ]);
  };
  const memberPath = (p: Principal) => `${path}/${p.principal_type}/${encodeURIComponent(p.principal_id)}`;
  const add = useMutation({
    mutationFn: async () => {
      const principals: Principal[] = selected.map((option) => JSON.parse(option.value) as Principal);
      if (id.trim()) principals.push({ principal_type: type, principal_id: id.trim() });
      for (const principal of principals) await api(memberPath(principal), { method: 'PUT' });
    },
    onSuccess: async () => {
      setSelected([]);
      setId('');
      await refresh();
    },
    onError: refresh,
  });
  const remove = useMutation({
    mutationFn: (p: Principal) => api(memberPath(p), { method: 'DELETE' }),
    onSuccess: refresh,
  });
  const nameOf = (p: Principal) =>
    p.principal_type === 'group'
      ? (groups.data?.find((group) => group.id === p.principal_id)?.name ?? p.principal_id)
      : (users.data?.find((user) => user.subject === p.principal_id)?.display_name ?? p.principal_id);
  const taken = new Set(list.data?.map((p) => JSON.stringify(p)));
  const options: ComboboxOption[] = [
    ...(users.data ?? []).map((user) => ({
      value: JSON.stringify({ principal_type: 'user', principal_id: user.subject }),
      label: user.display_name || user.subject,
      hint: user.subject,
    })),
    ...(groups.data ?? [])
      .filter((group) => group.id !== excludeGroup)
      .map((group) => ({
        value: JSON.stringify({ principal_type: 'group', principal_id: group.id }),
        label: group.name,
        hint: t('principals.group'),
      })),
  ].filter((option) => !taken.has(option.value));
  return (
    <div className="flex flex-col gap-3 border-t bg-surface-muted/30 p-4">
      <QueryFeedback query={list} />
      <ul className="flex flex-col gap-2">
        {list.data?.map((principal) => (
          <li key={JSON.stringify(principal)} className="flex items-center gap-2">
            <span className="flex-1">
              {nameOf(principal)} <span className="text-xs text-muted">{t(principal.principal_type === 'group' ? 'principals.kind.group' : 'principals.kind.user')}</span>
            </span>
            {canEdit && (
              <Button size="sm" disabled={remove.isPending} onClick={() => remove.mutate(principal)}>
                {t('principals.remove', { name: nameOf(principal) })}
              </Button>
            )}
          </li>
        ))}
      </ul>
      {list.data?.length === 0 && <p className="text-muted">{t('principals.nobody')}</p>}
      {canEdit && (
        <>
          <span id={`principals-${path}`} className="sr-only">
            {t('principals.label')}
          </span>
          <Combobox
            aria-labelledby={`principals-${path}`}
            multiple
            selected={selected}
            options={options}
            onChange={setSelected}
            placeholder={t('principals.choose')}
          />
          <div className="flex gap-2">
            <Select
              aria-label={t('principals.typeLabel')}
              value={type}
              onChange={(event) => setType(event.target.value as Principal['principal_type'])}
            >
              <option value="user">{t('principals.user')}</option>
              <option value="group">{t('principals.group')}</option>
            </Select>
            <Input
              aria-label={t('principals.identifier')}
              value={id}
              onChange={(event) => setId(event.target.value)}
              placeholder={type === 'user' ? t('principals.subject') : t('principals.groupId')}
            />
          </div>
          <Button disabled={add.isPending || (!selected.length && !id.trim())} onClick={() => add.mutate()}>
            {t('principals.add')}
          </Button>
        </>
      )}
    </div>
  );
}
