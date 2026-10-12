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

export const Route = createFileRoute('/_app/w/$workspaceId/settings/members')({ component: MembersPage });

const roles = [
  { value: 'owner', label: 'Owner', hint: 'Manages the workspace, its members, lists and libraries' },
  { value: 'member', label: 'Member', hint: 'Adds and edits content' },
  { value: 'visitor', label: 'Visitor', hint: 'Reads published content' },
];

function MembersPage() {
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
        toast.success(`${nameOf(variables.subject)} added`);
      } else toast.success('Role changed');
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
        title="Add people"
        description="People must be active users of the organization."
        actions={
          <Button
            variant="primary"
            disabled={setMember.isPending || !person.length}
            onClick={() => setMember.mutate({ subject: person[0].value, role })}
          >
            Add to workspace
          </Button>
        }
      >
        <SettingRow id="member-person" label="Person">
          <Combobox
            id="member-person"
            aria-labelledby="member-person-label"
            selected={person}
            options={options}
            loading={people.isPending}
            placeholder="Find a person…"
            onChange={setPerson}
          />
        </SettingRow>
        <SettingRow id="member-role" label="Role" hint={roles.find((entry) => entry.value === role)?.hint}>
          <Select id="member-role" value={role} onChange={(event) => setRole(event.target.value)}>
            {roles.map((entry) => (
              <option key={entry.value} value={entry.value}>
                {entry.label}
              </option>
            ))}
          </Select>
        </SettingRow>
      </SettingsSection>
      )}
      <SettingsSection title="Members" description="Owners manage the workspace; a workspace always keeps one owner.">
        <QueryFeedback query={list} />
        <ul className="flex flex-col divide-y">
          {sorted.map((member) => (
            <li key={member.subject} className="flex flex-wrap items-center gap-3 py-2.5">
              <Avatar name={nameOf(member.subject)} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px] font-medium">
                  {nameOf(member.subject)}
                  {member.subject === me.data?.subject && <span className="font-normal text-muted"> (you)</span>}
                </span>
                {nameOf(member.subject) !== member.subject && (
                  <span className="block truncate text-xs text-muted">{member.subject}</span>
                )}
              </span>
              {canManage ? (
                <>
                  <Select
                    aria-label={`Role of ${nameOf(member.subject)}`}
                    className="w-32"
                    value={member.role}
                    disabled={setMember.isPending}
                    onChange={(event) => setMember.mutate({ subject: member.subject, role: event.target.value })}
                  >
                    {roles.map((entry) => (
                      <option key={entry.value} value={entry.value}>
                        {entry.label}
                      </option>
                    ))}
                  </Select>
                  <Button size="sm" variant="ghost" onClick={() => setRemoving(member)}>
                    Remove
                  </Button>
                </>
              ) : (
                <span className="text-xs text-muted">{roles.find((entry) => entry.value === member.role)?.label ?? member.role}</span>
              )}
            </li>
          ))}
          {list.isSuccess && list.data.length === 0 && <li className="py-2 text-sm text-muted">No members yet.</li>}
        </ul>
      </SettingsSection>
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && setRemoving(undefined)}
        title={`Remove ${removing ? nameOf(removing.subject) : ''}?`}
        description={
          removing?.subject === me.data?.subject
            ? 'You lose access to this workspace unless a group or another grant still gives it to you.'
            : 'They lose access given by workspace membership. Explicit grants on lists or items stay.'
        }
        confirm="Remove member"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => removing && remove.mutate(removing.subject)}
      />
    </div>
  );
}
