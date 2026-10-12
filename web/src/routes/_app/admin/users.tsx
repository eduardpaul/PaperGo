import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { Pencil, Plus, Search, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys, meQuery } from '@/api/queries';
import type { UserProfile } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Avatar } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Field, QueryFeedback, useHasScope } from '@/features/admin/common';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/admin/users')({ component: UsersPage });
const userPath = (subject: string) => `/v1/identity/users/${encodeURIComponent(subject)}`;

function UsersPage() {
  const client = useQueryClient();
  const canRead = useHasScope('user.read');
  const canEdit = useHasScope('user.manage');
  const me = useQuery(meQuery);
  const users = useQuery({
    queryKey: ['admin', 'users'],
    enabled: canRead,
    queryFn: async () => (await api<UserProfile[]>('/v1/identity/users')).data,
  });
  const [filter, setFilter] = useState('');
  const [subject, setSubject] = useState('');
  const [editing, setEditing] = useState<UserProfile | 'new'>();
  const [deleting, setDeleting] = useState<UserProfile>();
  const format = useFormat();
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ['admin', 'users'] }),
      client.invalidateQueries({ queryKey: keys.users }),
      client.invalidateQueries({ queryKey: keys.people }),
      client.invalidateQueries({ queryKey: keys.me }),
    ]);
  };
  const lookup = useMutation({
    mutationFn: () => api<UserProfile>(userPath(subject.trim())).then((result) => result.data),
    onSuccess: setEditing,
  });
  const remove = useMutation({
    mutationFn: (user: UserProfile) => api(userPath(user.subject), { method: 'DELETE' }),
    onSuccess: async () => {
      setDeleting(undefined);
      await refresh();
    },
  });
  const text = filter.trim().toLowerCase();
  const shown = (users.data ?? []).filter((user) =>
    [user.subject, user.display_name, user.email].some((value) => value.toLowerCase().includes(text)),
  );
  if (!canRead) return <Alert>You need user.read to view accounts.</Alert>;
  return (
    <>
      <SettingsSection
        title="Users"
        description="Active accounts. Find an account by subject to inspect or reactivate it."
        actions={
          canEdit && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> New user
            </Button>
          )
        }
      >
        <div className="mb-4 flex flex-wrap gap-2">
          <Input
            aria-label="Find users"
            placeholder="Filter active accounts…"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            className="max-w-xs"
          />
          <form
            className="flex gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (subject.trim()) lookup.mutate();
            }}
          >
            <Input
              aria-label="Account subject"
              placeholder="Subject of any account"
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
              required
            />
            <Button type="submit" disabled={lookup.isPending}>
              <Search /> Find
            </Button>
          </form>
        </div>
        <QueryFeedback query={users} />
        <ul className="divide-y">
          {shown.map((user) => (
            <li key={user.subject} className="flex items-center gap-3 py-3">
              <Avatar name={user.display_name || user.subject} />
              <div className="min-w-0 flex-1">
                <p className="font-medium">
                  {user.display_name || user.subject}
                  {user.subject === me.data?.subject && ' (you)'}
                </p>
                <p className="truncate text-xs text-muted">
                  {user.subject} · {user.email}
                  {user.created_at && ` · since ${format.date(user.created_at)}`}
                </p>
              </div>
              <Button size="icon" variant="ghost" aria-label={`Edit ${user.subject}`} onClick={() => setEditing(user)}>
                <Pencil />
              </Button>
              {canEdit && user.subject !== me.data?.subject && (
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`Delete ${user.subject}`}
                  onClick={() => setDeleting(user)}
                >
                  <Trash2 />
                </Button>
              )}
            </li>
          ))}
        </ul>
        {!users.isPending && !users.error && shown.length === 0 && <EmptyState title="No users match" />}
      </SettingsSection>
      {editing && (
        <UserDialog
          user={editing === 'new' ? undefined : editing}
          canEdit={canEdit}
          onClose={() => setEditing(undefined)}
          onSaved={refresh}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={`Delete ${deleting?.subject ?? ''}?`}
        description="This anonymizes the account and permanently deletes its personal workspace and credentials. Shared content remains. This cannot be undone."
        confirm="Delete user"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

function UserDialog({
  user,
  canEdit,
  onClose,
  onSaved,
}: {
  user?: UserProfile;
  canEdit: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [subject, setSubject] = useState(user?.subject ?? '');
  const [name, setName] = useState(user?.display_name ?? '');
  const [email, setEmail] = useState(user?.email ?? '');
  const [active, setActive] = useState(user?.active ?? true);
  const [confirmStatus, setConfirmStatus] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      api(user ? userPath(user.subject) : '/v1/identity/users', {
        method: user ? 'PATCH' : 'POST',
        body: { subject: user ? undefined : subject.trim(), display_name: name.trim(), email: email.trim() },
      }),
    onSuccess: async () => {
      await onSaved();
      onClose();
    },
  });
  const status = useMutation({
    mutationFn: () =>
      api<{ active: boolean }>(`${userPath(subject)}/status`, { method: 'PUT', body: { active: !active } }),
    onSuccess: async (result) => {
      setActive(result.data.active);
      setConfirmStatus(false);
      await onSaved();
    },
  });
  return (
    <>
      <FormDialog
        open
        title={user ? `Account ${user.subject}` : 'New user'}
        submitLabel="Save account"
        submitDisabled={!canEdit || !!user?.deleted_at}
        pending={save.isPending || status.isPending}
        onClose={onClose}
        onSubmit={() => canEdit && save.mutate()}
      >
        <fieldset disabled={!canEdit || !!user?.deleted_at} className="flex flex-col gap-3">
          <Field id="user-subject" label="Subject">
            <Input
              id="user-subject"
              required
              disabled={!!user}
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
            />
          </Field>
          <Field id="user-display" label="Display name">
            <Input id="user-display" value={name} onChange={(event) => setName(event.target.value)} maxLength={200} />
          </Field>
          <Field id="user-email" label="Email">
            <Input id="user-email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} />
          </Field>
          {user && (
            <div className="flex items-center gap-3">
              <Badge tone={active ? 'success' : 'danger'}>{active ? 'Active' : 'Disabled'}</Badge>
              <Button disabled={status.isPending} onClick={() => setConfirmStatus(true)}>
                {active ? 'Disable account' : 'Activate account'}
              </Button>
            </div>
          )}
        </fieldset>
        {save.error && <Alert>{problemMessage(save.error)}</Alert>}
      </FormDialog>
      <ConfirmDialog
        open={confirmStatus}
        onOpenChange={setConfirmStatus}
        title={active ? 'Disable this account?' : 'Activate this account?'}
        description={
          active
            ? 'The user loses access immediately.'
            : 'The user can access PaperGo with their assigned scopes and permissions.'
        }
        confirm={active ? 'Disable' : 'Activate'}
        busy={status.isPending}
        error={status.error}
        onConfirm={() => status.mutate()}
      />
    </>
  );
}
