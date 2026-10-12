import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { members } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { SettingRow, SettingsSection } from '@/features/settings/section';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/members')({ component: MembersPage });

function MembersPage() {
  const { workspaceId } = Route.useParams();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['members', workspaceId], queryFn: () => members(workspaceId) });
  const [subject, setSubject] = useState('');
  const [role, setRole] = useState('member');
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['members', workspaceId] });
  const add = useMutation({
    mutationFn: () => api(`/v1/workspaces/${workspaceId}/members`, { method: 'POST', body: { subject: subject.trim(), role } }),
    onSuccess: async () => {
      setSubject('');
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (member: string) => api(`/v1/workspaces/${workspaceId}/members/${encodeURIComponent(member)}`, { method: 'DELETE' }),
    onSuccess: refresh,
  });
  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title="Add a member"
        description="Roles are owner, member and visitor."
        actions={
          <Button variant="primary" disabled={add.isPending || !subject.trim()} onClick={() => add.mutate()}>
            Add
          </Button>
        }
      >
        <SettingRow id="member-subject" label="Subject">
          <Input id="member-subject" value={subject} onChange={(event) => setSubject(event.target.value)} />
        </SettingRow>
        <SettingRow id="member-role" label="Role">
          <Select id="member-role" value={role} onChange={(event) => setRole(event.target.value)}>
            <option value="owner">Owner</option>
            <option value="member">Member</option>
            <option value="visitor">Visitor</option>
          </Select>
        </SettingRow>
      </SettingsSection>
      <SettingsSection title="Members">
        <ul className="flex flex-col gap-2">
          {(list.data ?? []).map((member) => (
            <li key={member.subject} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <span className="min-w-0 flex-1 truncate text-[13px]">{member.subject}</span>
              <span className="text-xs text-muted">{member.role}</span>
              <Button size="sm" variant="danger" disabled={remove.isPending} onClick={() => remove.mutate(member.subject)}>
                Remove
              </Button>
            </li>
          ))}
          {list.isFetched && (list.data?.length ?? 0) === 0 && <li className="text-sm text-muted">No members yet.</li>}
        </ul>
      </SettingsSection>
    </div>
  );
}
