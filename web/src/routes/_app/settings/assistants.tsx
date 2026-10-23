import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { api } from '@/api/http';
import { collectionChildren, scopes, tokens, workspacesQuery } from '@/api/queries';
import type { AccessToken, CreatedAccessToken, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ConfirmDialog, CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/assistants')({
  component: AssistantsPage,
});

/** What a read-only connection may do; read and write adds list.write. */
export const ReadScopes = [
  'workspace.read',
  'list.read',
  'contentType.read',
  'document.read',
  'taxonomy.read',
  'user.read',
];
/** The most places one connection can be limited to. */
export const MaxPlaces = 50;

/** The MCP endpoint, served beside the web app on the same host. */
export function endpoint() {
  return `${window.location.origin}/v1/mcp`;
}

export function claudeCode(secret: string) {
  return `claude mcp add --transport http papergo ${endpoint()} --header "Authorization: Bearer ${secret}"`;
}

export function jsonConfig(secret: string) {
  return JSON.stringify({
    mcpServers: {
      papergo: {
        type: 'http',
        url: endpoint(),
        headers: { Authorization: `Bearer ${secret}` },
      },
    },
  });
}

/** "Read only · Everything" or "Read and write · 2 places". */
export function summary(token: AccessToken) {
  const access = token.scopes?.includes('list.write') ? 'Read and write' : 'Read only';
  const places = token.resources?.length ?? 0;
  return `${access} · ${places === 0 ? 'Everything' : places === 1 ? '1 place' : `${places} places`}`;
}

function AssistantsPage() {
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['tokens'], queryFn: tokens });
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes });
  const [name, setName] = useState('AI assistant');
  const [write, setWrite] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [limited, setLimited] = useState(false);
  const [places, setPlaces] = useState<string[]>([]);
  const [days, setDays] = useState('90');
  const [created, setCreated] = useState<CreatedAccessToken | null>(null);
  const [revoking, setRevoking] = useState<AccessToken | null>(null);
  const result = useRef<HTMLDivElement>(null);
  // The new connection's setup appears below the form; bring it into view.
  useEffect(() => {
    if (created) result.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
  }, [created]);

  const held = mine.data ?? [];
  const canRead = held.includes('list.read');
  const canWrite = held.includes('list.write');
  const expires = Number(days);
  const validExpiry = Number.isInteger(expires) && expires >= 1 && expires <= 3650;
  const validPlaces = !limited || (places.length > 0 && places.length <= MaxPlaces);
  const issue = useMutation({
    mutationFn: () =>
      api<CreatedAccessToken>('/v1/identity/me/tokens', {
        method: 'POST',
        body: {
          name: name.trim(),
          scopes: [...ReadScopes, ...(write ? ['list.write'] : [])].filter((scope) => held.includes(scope)),
          resources: limited ? places : undefined,
          expires_in_days: expires,
        },
      }).then((result) => result.data),
    onSuccess: async (connection) => {
      setCreated(connection);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/v1/identity/me/tokens/${id}`, { method: 'DELETE' }),
    onSuccess: async () => {
      setRevoking(null);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });
  // Any token that reads lists can connect an assistant.
  const connections = (list.data ?? []).filter((token) => token.scopes?.includes('list.read'));

  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title="Connect an assistant"
        description="Let an AI assistant, such as Claude, find and read your work in PaperGo, and change it if you allow. It never gets more access than you have."
        actions={
          <Button
            variant="primary"
            disabled={issue.isPending || !canRead || !name.trim() || !validExpiry || !validPlaces}
            onClick={() => issue.mutate()}
          >
            Create connection
          </Button>
        }
      >
        <SettingRow id="assistant-name" label="Name" hint="So you recognise it later, such as the assistant it is for.">
          <Input id="assistant-name" value={name} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow id="assistant-access" label="Access">
          <div role="radiogroup" aria-labelledby="assistant-access-label" className="flex flex-col gap-1.5">
            <label className="flex items-center gap-2 text-[13px]">
              <input type="radio" name="assistant-access" checked={!write} onChange={() => setWrite(false)} />
              Read only
            </label>
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="radio"
                name="assistant-access"
                checked={write}
                disabled={!canWrite}
                onChange={() => setWrite(true)}
              />
              Read and write: create and edit items
            </label>
          </div>
        </SettingRow>
        <button
          type="button"
          className="flex items-center gap-1 py-2 text-[13px] font-medium text-muted hover:text-foreground"
          aria-expanded={advanced}
          onClick={() => setAdvanced((open) => !open)}
        >
          {advanced ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />} Advanced options
        </button>
        {advanced && (
          <>
            <SettingRow id="assistant-reach" label="Reach">
              <div role="radiogroup" aria-labelledby="assistant-reach-label" className="flex flex-col gap-1.5">
                <label className="flex items-center gap-2 text-[13px]">
                  <input type="radio" name="assistant-reach" checked={!limited} onChange={() => setLimited(false)} />
                  Everything I can access
                </label>
                <label className="flex items-center gap-2 text-[13px]">
                  <input type="radio" name="assistant-reach" checked={limited} onChange={() => setLimited(true)} />
                  Only selected workspaces, lists and libraries
                </label>
              </div>
            </SettingRow>
            {limited && (
              <SettingRow
                id="assistant-places"
                label="Places"
                hint={
                  places.length > MaxPlaces
                    ? `Choose at most ${MaxPlaces}.`
                    : places.length === 0
                      ? 'Choose at least one. A workspace includes the lists and libraries added to it later.'
                      : 'A workspace includes the lists and libraries added to it later.'
                }
              >
                <PlacePicker selected={places} onChange={setPlaces} />
              </SettingRow>
            )}
            <SettingRow id="assistant-days" label="Expires in days">
              <Input
                id="assistant-days"
                inputMode="numeric"
                value={days}
                onChange={(event) => setDays(event.target.value)}
              />
            </SettingRow>
          </>
        )}
        {mine.isSuccess && !canRead && (
          <p className="text-sm text-muted">Reading lists is required to connect an assistant.</p>
        )}
        {issue.isError && <p className="text-sm text-danger">{problemMessage(issue.error)}</p>}
      </SettingsSection>
      {created && (
        <div ref={result}>
          <SettingsSection
            title="Connection created"
            description="Add it to your assistant now: the token is shown only once, and anyone holding it acts as you within this connection."
          >
            <div className="flex flex-col gap-3">
              <SettingRow id="assistant-endpoint" label="Address">
                <CopyField label="Address" value={endpoint()} />
              </SettingRow>
              <SettingRow id="assistant-token" label="Token">
                <CopyField label="Token" value={created.secret} />
              </SettingRow>
              <SettingRow id="assistant-claude" label="Claude Code">
                <CopyField label="Claude Code command" value={claudeCode(created.secret)} />
              </SettingRow>
              <SettingRow
                id="assistant-json"
                label="Other assistants"
                hint="For clients configured with JSON, such as Cursor or VS Code."
              >
                <CopyField label="JSON configuration" value={jsonConfig(created.secret)} />
              </SettingRow>
            </div>
          </SettingsSection>
        </div>
      )}
      <SettingsSection
        title="Your connections"
        description="Every access token that can read lists can connect an assistant."
      >
        <ul className="flex flex-col gap-2">
          {connections.map((token) => (
            <li key={token.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium">{token.name}</p>
                <p className="text-xs text-muted">
                  {summary(token)} ·{' '}
                  {token.last_used_at ? `last used ${format.date(token.last_used_at)}` : 'never used'} · expires{' '}
                  {token.expires_at ? format.date(token.expires_at) : 'never'}
                </p>
              </div>
              <Button size="sm" variant="danger" onClick={() => setRevoking(token)}>
                Revoke
              </Button>
            </li>
          ))}
          {list.isFetched && connections.length === 0 && <li className="text-sm text-muted">No connections yet.</li>}
        </ul>
      </SettingsSection>
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        title={`Revoke ${revoking?.name ?? 'this connection'}?`}
        description="Assistants using it stop working at once. This cannot be undone; create a new connection to reconnect."
        confirm="Revoke"
        busy={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </div>
  );
}

/** Workspaces with their lists and libraries; ticking a workspace takes all of it. */
function PlacePicker({ selected, onChange }: { selected: string[]; onChange: (ids: string[]) => void }) {
  const workspaces = useQuery(workspacesQuery);
  const [filter, setFilter] = useState('');
  // Taking a whole workspace drops its lists and libraries ticked one by one.
  const toggle = (id: string, on: boolean, drop: string[] = []) =>
    onChange(
      on ? [...selected.filter((other) => !drop.includes(other)), id] : selected.filter((other) => other !== id),
    );
  const shown = (workspaces.data ?? []).filter((workspace) => workspace.kind === 'workspace');
  return (
    <div className="flex flex-col gap-2">
      <Input
        aria-label="Filter places"
        placeholder="Filter by name"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      <div className="flex max-h-72 flex-col gap-1 overflow-auto rounded-md border p-2">
        {shown.map((workspace) => (
          <WorkspacePlaces
            key={workspace.id}
            workspace={workspace}
            filter={filter}
            selected={selected}
            onToggle={toggle}
          />
        ))}
        {workspaces.isFetched && shown.length === 0 && <p className="text-sm text-muted">No workspaces.</p>}
      </div>
      <p className="text-xs text-muted">
        {selected.length === 1 ? '1 place selected' : `${selected.length} places selected`}
      </p>
    </div>
  );
}

function WorkspacePlaces({
  workspace,
  filter,
  selected,
  onToggle,
}: {
  workspace: Resource;
  filter: string;
  selected: string[];
  onToggle: (id: string, on: boolean, drop?: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const collections = useQuery({
    queryKey: ['resources', workspace.id, 'collections'],
    queryFn: () => collectionChildren(workspace.id),
    enabled: open || filter !== '',
  });
  const needle = filter.trim().toLowerCase();
  const matching = useMemo(
    () => (collections.data ?? []).filter((collection) => !needle || collection.name.toLowerCase().includes(needle)),
    [collections.data, needle],
  );
  const whole = selected.includes(workspace.id);
  if (needle && !workspace.name.toLowerCase().includes(needle) && matching.length === 0) return null;
  return (
    <div>
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-label={`${open ? 'Hide' : 'Show'} ${workspace.name}'s lists and libraries`}
          aria-expanded={open}
          className="text-muted"
          onClick={() => setOpen((value) => !value)}
        >
          {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        </button>
        <label className="flex items-center gap-2 text-[13px] font-medium">
          <input
            type="checkbox"
            checked={whole}
            onChange={(event) =>
              onToggle(
                workspace.id,
                event.target.checked,
                (collections.data ?? []).map((collection) => collection.id),
              )
            }
          />
          {workspace.name}
        </label>
      </div>
      {(open || needle) && (
        <div className="ml-6 flex flex-col gap-1 py-1">
          {matching.map((collection) => (
            <label key={collection.id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={whole || selected.includes(collection.id)}
                disabled={whole}
                onChange={(event) => onToggle(collection.id, event.target.checked)}
              />
              {collection.name}
              <span className="text-xs text-muted">{collection.kind === 'library' ? 'Library' : 'List'}</span>
            </label>
          ))}
          {collections.isFetched && matching.length === 0 && (
            <p className="text-xs text-muted">No lists or libraries.</p>
          )}
        </div>
      )}
    </div>
  );
}
