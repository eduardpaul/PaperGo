import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { meQuery, members, peopleQuery } from '@/api/queries';
import type { WorkspaceMember } from '@/api/types';
import { Avatar } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import { Combobox, type ComboboxOption } from '@/components/ui/combobox';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { usePeopleNames } from '@/features/fields/people';
import { useAccess } from '@/features/lists/access';
import { ConfirmDialog, SettingRow, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/members')({ component: MembersPage });

const roles = ['owner', 'member', 'visitor'] as const;
const knownRole = (role: string) => roles.find((entry) => entry === role);

function MembersPage() {
  const { t } = useTranslation(['workspaces', 'common']);
  const { workspaceId } = Route.useParams();
  const queryClient = useQueryClient();
  const me = useQuery(meQuery);
  const { canManage } = useAccess(workspaceId);
  const names = usePeopleNames();
  const people = useQuery(peopleQuery);
  const list = useQuery({ queryKey: ['members', workspaceId], queryFn: () => members(workspaceId) });
  const [person, setPerson] = useState<ComboboxOption[]>([]);
  const [role, setRole] = useState('member');
  const [removing, setRemoving] = useState<WorkspaceMember>();
  const nameOf = (subject: string) => names.get(subject) ?? subject;
  const current = new Set((list.data ?? []).map((member) => member.subject));
  const options = (people.data ?? [])
    .filter((subject) => !current.has(subject))
    .map((subject) => ({ value: subject, label: nameOf(subject), hint: nameOf(subject) !== subject ? subject : undefined }));
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['members', workspaceId] });
  const setMember = useMutation({
    mutationFn: ({ subject, role }: { subject: string; role: string }) =>
      api(`/v1/workspaces/${workspaceId}/members`, { method: 'POST', body: { subject, role } }),
    onSuccess: async (_result, variables) => {
      if (person[0]?.value === variables.subject) {
        setPerson([]);
        toast.success(t('members.added', { name: nameOf(variables.subject) }));
      } else toast.success(t('members.roleChanged'));
      await refresh();
    },
  });
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: (member: string) =>
      api(`/v1/workspaces/${workspaceId}/members/${encodeURIComponent(member)}`, { method: 'DELETE' }),
    onSuccess: async () => {
      setRemoving(undefined);
      await refresh();
    },
  });
  const sorted = [...(list.data ?? [])].sort((a, b) => nameOf(a.subject).localeCompare(nameOf(b.subject)));
  return (
    <div className="flex flex-col gap-5">
      {canManage && (
      <SettingsSection
        title={t('members.addTitle')}
        description={t('members.addDescription')}
        actions={
          <Button
            variant="primary"
            disabled={setMember.isPending || !person.length}
            onClick={() => setMember.mutate({ subject: person[0].value, role })}
          >
            {t('members.add')}
          </Button>
        }
      >
        <SettingRow id="member-person" label={t('members.person')}>
          <Combobox
            id="member-person"
            aria-labelledby="member-person-label"
            selected={person}
            options={options}
            loading={people.isPending}
            placeholder={t('members.findPerson')}
            onChange={setPerson}
          />
        </SettingRow>
        <SettingRow id="member-role" label={t('members.role')} hint={knownRole(role) && t(`members.roles.${knownRole(role)!}.hint`)}>
          <Select id="member-role" value={role} onChange={(event) => setRole(event.target.value)}>
            {roles.map((entry) => (
              <option key={entry} value={entry}>
                {t(`members.roles.${entry}.label`)}
              </option>
            ))}
          </Select>
        </SettingRow>
      </SettingsSection>
      )}
      <SettingsSection title={t('settings.members')} description={t('members.description')}>
        <QueryFeedback query={list} />
        <ul className="flex flex-col divide-y">
          {sorted.map((member) => (
            <li key={member.subject} className="flex flex-wrap items-center gap-3 py-2.5">
              <Avatar name={nameOf(member.subject)} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px] font-medium">
                  {nameOf(member.subject)}
                  {member.subject === me.data?.subject && <span className="font-normal text-muted"> {t('members.you')}</span>}
                </span>
                {nameOf(member.subject) !== member.subject && (
                  <span className="block truncate text-xs text-muted">{member.subject}</span>
                )}
              </span>
              {canManage ? (
                <>
                  <Select
                    aria-label={t('members.roleOf', { name: nameOf(member.subject) })}
                    className="w-32"
                    value={member.role}
                    disabled={setMember.isPending}
                    onChange={(event) => setMember.mutate({ subject: member.subject, role: event.target.value })}
                  >
                    {roles.map((entry) => (
                      <option key={entry} value={entry}>
                        {t(`members.roles.${entry}.label`)}
                      </option>
                    ))}
                  </Select>
                  <Button size="sm" variant="ghost" onClick={() => setRemoving(member)}>
                    {t('common:actions.removeItem')}
                  </Button>
                </>
              ) : (
                <span className="text-xs text-muted">{knownRole(member.role) ? t(`members.roles.${knownRole(member.role)!}.label`) : member.role}</span>
              )}
            </li>
          ))}
          {list.isSuccess && list.data.length === 0 && <li className="py-2 text-sm text-muted">{t('members.none')}</li>}
        </ul>
      </SettingsSection>
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && setRemoving(undefined)}
        title={t('members.removeTitle', { name: removing ? nameOf(removing.subject) : '' })}
        description={
          removing?.subject === me.data?.subject
            ? t('members.removeSelf')
            : t('members.removeOther')
        }
        confirm={t('members.removeConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => removing && remove.mutate(removing.subject)}
      />
    </div>
  );
}
