import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { Check, ChevronDown, ChevronRight, CircleAlert, LoaderCircle } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { mcpEndpoint } from '@/api/auth';
import { api } from '@/api/http';
import { collectionChildren, resourceQuery, scopes, tokens, workspacesQuery } from '@/api/queries';
import type { AccessToken, CreatedAccessToken, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ConfirmDialog, CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { i18n, Trans, useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/assistants')({
  component: AssistantsPage,
});

/** What a read-only connection may do; read and write adds item.write. */
export const ReadScopes = [
  'workspace.read',
  'item.read',
  'contentType.read',
  'document.read',
  'taxonomy.read',
  'user.read',
];
/** The most places one connection can be limited to. */
export const MaxPlaces = 50;

/** A workspace, list or library a connection is limited to. */
export interface Place {
  id: string;
  name: string;
  kind: Resource['kind'];
  workspace?: string;
}

/** Setup for each kind of client, which all send the token as a header. */
export function clientSetup(endpoint: string, secret: string) {
  const headers = { Authorization: `Bearer ${secret}` };
  return {
    claudeCode: `claude mcp add --transport http papergo ${endpoint} --header "Authorization: Bearer ${secret}"`,
    cursor: JSON.stringify({ mcpServers: { papergo: { url: endpoint, headers } } }),
    vscode: JSON.stringify({ servers: { papergo: { type: 'http', url: endpoint, headers } } }),
    json: JSON.stringify({ mcpServers: { papergo: { type: 'http', url: endpoint, headers } } }),
  };
}

const McpPath = '/v1/mcp';

export type CheckResult = { ok: true } | { ok: false; reason: string };

/**
 * Asks this PaperGo's MCP endpoint, with the new token, to start a session,
 * as an assistant would. It asks on the page's own origin, which serves the
 * same endpoint as the public address assistants are given.
 */
export async function checkConnection(secret: string): Promise<CheckResult> {
  let response: Response;
  try {
    response = await fetch(McpPath, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${secret}`,
        'Content-Type': 'application/json',
        Accept: 'application/json, text/event-stream',
        'MCP-Protocol-Version': '2025-06-18',
      },
      body: JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'initialize',
        params: {
          protocolVersion: '2025-06-18',
          capabilities: {},
          clientInfo: { name: 'PaperGo settings', version: '1' },
        },
      }),
    });
  } catch {
    return { ok: false, reason: i18n.t('settings:assistants.check.nothingAnswered', { endpoint: McpPath }) };
  }
  if (response.status === 404) return { ok: false, reason: i18n.t('settings:assistants.check.notFound', { endpoint: McpPath }) };
  if (response.status === 401) return { ok: false, reason: i18n.t('settings:assistants.check.refused') };
  if (!response.ok) return { ok: false, reason: i18n.t('settings:assistants.check.status', { status: response.status }) };
  try {
    const body = (await response.json()) as { result?: { serverInfo?: unknown } };
    if (body.result?.serverInfo) return { ok: true };
  } catch {
    // Reported below.
  }
  return { ok: false, reason: i18n.t('settings:assistants.check.notMcp', { endpoint: McpPath }) };
}

/** The scopes a connection gets: reading, and item.write for read and write. */
export function connectionScopes(write: boolean, held: string[]) {
  return [...ReadScopes, ...(write ? ['item.write'] : [])].filter((scope) => held.includes(scope));
}

/**
 * What a token may do: read only, or read and write, as connections are
 * made here. A token made elsewhere can hold more, such as list.manage, and
 * those scopes are named so it never looks narrower than it is.
 */
export function accessLabel(scopes: string[]) {
  const write = scopes.includes('item.write');
  const more = scopes.filter((scope) => scope !== 'item.write' && !ReadScopes.includes(scope));
  if (more.length === 0) return i18n.t(write ? 'settings:assistants.accessReadWrite' : 'settings:assistants.accessReadOnly');
  return i18n.t(write ? 'settings:assistants.accessReadWritePlus' : 'settings:assistants.accessReadPlus', { scopes: more.join(', ') });
}

export function placesLabel(places: Place[] | null) {
  if (places === null) return i18n.t('settings:assistants.everything');
  return places
    .map((place) => (place.workspace ? i18n.t('settings:assistants.placeIn', { name: place.name, workspace: place.workspace }) : place.name))
    .join(', ');
}

function expiryDate(days: number) {
  const date = new Date();
  date.setDate(date.getDate() + days);
  return date.toISOString();
}

export function expired(token: AccessToken, now = Date.now()) {
  return token.expires_at !== null && new Date(token.expires_at).getTime() <= now;
}

function AssistantsPage() {
  const { t } = useTranslation('settings');
  const endpoint = mcpEndpoint();
  return (
    <div className="flex flex-col gap-5">
      {endpoint ? (
        <Connect endpoint={endpoint} />
      ) : (
        <SettingsSection
          title={t('assistants.connectTitle')}
          description={t('assistants.unavailableDescription')}
        >
          <p className="text-sm">
            <Trans t={t} i18nKey="assistants.unavailable" components={{ code: <code /> }} />
          </p>
        </SettingsSection>
      )}
      <Connections />
    </div>
  );
}

function Connect({ endpoint }: { endpoint: string }) {
  const { t } = useTranslation(['settings', 'common']);
  const format = useFormat();
  const queryClient = useQueryClient();
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes });
  // Until the user names the connection, its default name follows the screen's language.
  const [typedName, setName] = useState<string>();
  const name = typedName ?? t('assistants.defaultName');
  const [write, setWrite] = useState(false);
  const [limited, setLimited] = useState(false);
  const [places, setPlaces] = useState<Place[]>([]);
  const [advanced, setAdvanced] = useState(false);
  const [days, setDays] = useState('90');
  const [created, setCreated] = useState<CreatedAccessToken | null>(null);
  const [check, setCheck] = useState<CheckResult | 'checking' | null>(null);
  const result = useRef<HTMLDivElement>(null);

  const held = mine.data ?? [];
  const canRead = held.includes('item.read');
  const canWrite = held.includes('item.write');
  const expires = Number(days);
  const validExpiry = Number.isInteger(expires) && expires >= 1 && expires <= 3650;
  const validPlaces = !limited || (places.length > 0 && places.length <= MaxPlaces);

  const verify = useCallback(
    async (secret: string) => {
      setCheck('checking');
      setCheck(await checkConnection(secret));
      // The check used the token, so its last use changed.
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
    [queryClient],
  );
  const issue = useMutation({
    mutationFn: () =>
      api<CreatedAccessToken>('/v1/identity/me/tokens', {
        method: 'POST',
        body: {
          name: name.trim(),
          scopes: connectionScopes(write, held),
          resources: limited ? places.map((place) => place.id) : undefined,
          expires_in_days: expires,
        },
      }).then((response) => response.data),
    onSuccess: async (connection) => {
      setCreated(connection);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
      void verify(connection.secret);
    },
  });
  // The new connection appears below the form; bring it into view.
  useEffect(() => {
    if (created) result.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
  }, [created]);

  return (
    <>
      <SettingsSection
        title={t('assistants.connectTitle')}
        description={t('assistants.connectDescription')}
        actions={
          <Button
            variant="primary"
            disabled={issue.isPending || !canRead || !name.trim() || !validExpiry || !validPlaces}
            onClick={() => issue.mutate()}
          >
            {t('assistants.create')}
          </Button>
        }
      >
        <SettingRow id="assistant-name" label={t('common:fields.name')} hint={t('assistants.nameHint')}>
          <Input id="assistant-name" value={name} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow
          id="assistant-access"
          label={t('assistants.access')}
          hint={t('assistants.accessHint')}
        >
          <div role="radiogroup" aria-labelledby="assistant-access-label" className="flex flex-col gap-1.5">
            <label className="flex items-center gap-2 text-[13px]">
              <input type="radio" name="assistant-access" checked={!write} onChange={() => setWrite(false)} />
              {t('assistants.readOnlyOption')}
            </label>
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="radio"
                name="assistant-access"
                checked={write}
                disabled={!canWrite}
                onChange={() => setWrite(true)}
              />
              {t('assistants.readWriteOption')}
            </label>
          </div>
        </SettingRow>
        <SettingRow id="assistant-reach" label={t('assistants.reach')}>
          <div role="radiogroup" aria-labelledby="assistant-reach-label" className="flex flex-col gap-1.5">
            <label className="flex items-center gap-2 text-[13px]">
              <input type="radio" name="assistant-reach" checked={!limited} onChange={() => setLimited(false)} />
              {t('assistants.everythingOption')}
            </label>
            <label className="flex items-center gap-2 text-[13px]">
              <input type="radio" name="assistant-reach" checked={limited} onChange={() => setLimited(true)} />
              {t('assistants.selectedOption')}
            </label>
          </div>
        </SettingRow>
        {limited && (
          <SettingRow
            id="assistant-places"
            label={t('assistants.places')}
            hint={
              places.length > MaxPlaces
                ? t('assistants.placesMax', { max: MaxPlaces })
                : places.length === 0
                  ? t('assistants.placesNone')
                  : t('assistants.placesHint')
            }
          >
            <PlacePicker selected={places} onChange={setPlaces} />
          </SettingRow>
        )}
        <button
          type="button"
          className="flex items-center gap-1 py-2 text-[13px] font-medium text-muted hover:text-foreground"
          aria-expanded={advanced}
          onClick={() => setAdvanced((open) => !open)}
        >
          {advanced ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />} {t('assistants.advanced')}
        </button>
        {advanced && (
          <SettingRow id="assistant-days" label={t('tokens.expiresInDays')}>
            <Input
              id="assistant-days"
              inputMode="numeric"
              value={days}
              onChange={(event) => setDays(event.target.value)}
            />
          </SettingRow>
        )}
        <dl
          aria-label={t('assistants.summary')}
          className="mt-2 grid gap-1 rounded-md bg-surface-muted/60 px-3 py-2.5 text-[13px] sm:grid-cols-[6rem_1fr]"
        >
          <dt className="text-muted">{t('assistants.access')}</dt>
          <dd>{accessLabel(connectionScopes(write, held))}</dd>
          <dt className="text-muted">{t('assistants.reach')}</dt>
          <dd>{limited ? (places.length ? placesLabel(places) : t('assistants.noPlaces')) : placesLabel(null)}</dd>
          <dt className="text-muted">{t('assistants.expires')}</dt>
          <dd>{validExpiry ? format.date(expiryDate(expires)) : t('assistants.chooseDays')}</dd>
        </dl>
        {mine.isSuccess && !canRead && (
          <p className="mt-2 text-sm text-muted">{t('assistants.readRequired')}</p>
        )}
        {issue.isError && <p className="mt-2 text-sm text-danger">{problemMessage(issue.error)}</p>}
      </SettingsSection>
      {created && (
        <div ref={result}>
          <SettingsSection
            title={t('assistants.setUp', { name: created.token.name })}
            description={t('assistants.setUpDescription')}
          >
            <ol className="mb-4 flex flex-col gap-1.5 text-[13px]">
              <li className="flex items-center gap-2">
                <Check className="size-4 text-success" /> {t('assistants.tokenCreated')}
              </li>
              <li className="flex items-center gap-2" aria-live="polite">
                {check === 'checking' || check === null ? (
                  <>
                    <LoaderCircle className="size-4 animate-spin text-muted" /> {t('assistants.checking')}
                  </>
                ) : check.ok ? (
                  <>
                    <Check className="size-4 text-success" /> {t('assistants.answered')}
                  </>
                ) : (
                  <>
                    <CircleAlert className="size-4 text-danger" />
                    <span>{t('assistants.notReachable', { reason: check.reason })}</span>
                    <Button size="sm" onClick={() => void verify(created.secret)}>
                      {t('assistants.checkAgain')}
                    </Button>
                  </>
                )}
              </li>
            </ol>
            <ClientSetup endpoint={endpoint} secret={created.secret} />
          </SettingsSection>
        </div>
      )}
    </>
  );
}

// Product names, the same in every language.
const clients = [
  { value: 'claude-code', name: 'Claude Code' },
  { value: 'cursor', name: 'Cursor' },
  { value: 'vscode', name: 'VS Code' },
];

function ClientSetup({ endpoint, secret }: { endpoint: string; secret: string }) {
  const { t } = useTranslation('settings');
  const setup = clientSetup(endpoint, secret);
  return (
    <div className="flex flex-col gap-3">
      <SettingRow id="assistant-endpoint" label={t('assistants.address')}>
        <CopyField label={t('assistants.address')} value={endpoint} />
      </SettingRow>
      <SettingRow id="assistant-token" label={t('assistants.token')}>
        <CopyField label={t('assistants.token')} value={secret} />
      </SettingRow>
      <Tabs defaultValue="claude-code" className="rounded-md border">
        <TabsList aria-label={t('assistants.client')}>
          {clients.map((client) => (
            <TabsTrigger key={client.value} value={client.value}>
              {client.name}
            </TabsTrigger>
          ))}
          <TabsTrigger value="other">{t('assistants.other')}</TabsTrigger>
        </TabsList>
        <TabsContent value="claude-code" className="flex flex-col gap-2 p-4 text-[13px]">
          <p>{t('assistants.claudeCodeHint')}</p>
          <CopyField label={t('assistants.claudeCodeLabel')} value={setup.claudeCode} />
        </TabsContent>
        <TabsContent value="cursor" className="flex flex-col gap-2 p-4 text-[13px]">
          <p>
            <Trans t={t} i18nKey="assistants.cursorHint" components={{ code: <code /> }} />
          </p>
          <CopyField label={t('assistants.cursorLabel')} value={setup.cursor} />
        </TabsContent>
        <TabsContent value="vscode" className="flex flex-col gap-2 p-4 text-[13px]">
          <p>
            <Trans t={t} i18nKey="assistants.vscodeHint" components={{ code: <code /> }} />
          </p>
          <CopyField label={t('assistants.vscodeLabel')} value={setup.vscode} />
        </TabsContent>
        <TabsContent value="other" className="flex flex-col gap-2 p-4 text-[13px]">
          <p>
            <Trans t={t} i18nKey="assistants.otherHint" components={{ code: <code /> }} />
          </p>
          <CopyField label={t('assistants.jsonLabel')} value={setup.json} />
        </TabsContent>
      </Tabs>
      <p className="text-xs text-muted">{t('assistants.oauthNote')}</p>
    </div>
  );
}

function Connections() {
  const { t } = useTranslation('settings');
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['tokens'], queryFn: tokens });
  const [revoking, setRevoking] = useState<AccessToken | null>(null);
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/v1/identity/me/tokens/${id}`, { method: 'DELETE' }),
    onSuccess: async () => {
      setRevoking(null);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });
  // Any token that reads lists can connect an assistant.
  const connections = (list.data ?? []).filter((token) => token.scopes?.includes('item.read'));
  return (
    <SettingsSection
      title={t('assistants.connectionsTitle')}
      description={t('assistants.connectionsDescription')}
    >
      <ul className="flex flex-col gap-2">
        {connections.map((token) => {
          const over = expired(token);
          return (
            <li key={token.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 truncate text-[13px] font-medium">
                  {token.name}
                  {over ? <Badge tone="danger">{t('assistants.expired')}</Badge> : !token.last_used_at && <Badge>{t('assistants.notUsed')}</Badge>}
                </p>
                <p className="text-xs text-muted">
                  {accessLabel(token.scopes ?? [])} · <TokenPlaces ids={token.resources ?? []} />
                </p>
                <p className="text-xs text-muted">
                  {token.last_used_at ? t('assistants.lastUsed', { date: format.date(token.last_used_at) }) : t('assistants.neverUsed')} ·{' '}
                  {token.expires_at
                    ? t(over ? 'assistants.expiredOn' : 'assistants.expiresOn', { date: format.date(token.expires_at) })
                    : t('assistants.neverExpires')}
                </p>
              </div>
              <Button size="sm" variant="danger" onClick={() => setRevoking(token)}>
                {t('assistants.revoke')}
              </Button>
            </li>
          );
        })}
        {list.isFetched && connections.length === 0 && <li className="text-sm text-muted">{t('assistants.noConnections')}</li>}
      </ul>
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        title={t('assistants.revokeTitle', { name: revoking?.name ?? t('assistants.thisConnection') })}
        description={t('assistants.revokeDescription')}
        confirm={t('assistants.revoke')}
        busy={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </SettingsSection>
  );
}

/** The names of the places a token reaches; places since removed or out of reach are counted. */
function TokenPlaces({ ids }: { ids: string[] }) {
  const { t } = useTranslation('settings');
  const resources = useQueries({ queries: ids.map((id) => ({ ...resourceQuery(id), retry: false })) });
  if (ids.length === 0) return <>{t('assistants.everything')}</>;
  const names = resources.flatMap((r) => (r.data ? [r.data.name] : []));
  const missing = resources.filter((r) => r.isError).length;
  const pending = resources.some((r) => r.isPending);
  const parts = [...names];
  if (missing) parts.push(t('assistants.unavailablePlaces', { count: missing }));
  if (pending && parts.length === 0) return <>{t('assistants.placeCount', { count: ids.length })}</>;
  return <>{parts.join(', ')}</>;
}

/** Workspaces with their lists and libraries; ticking a workspace takes all of it. */
function PlacePicker({ selected, onChange }: { selected: Place[]; onChange: (places: Place[]) => void }) {
  const { t } = useTranslation('settings');
  const workspaces = useQuery(workspacesQuery);
  const [filter, setFilter] = useState('');
  // Taking a whole workspace drops its lists and libraries ticked one by one.
  const toggle = (place: Place, on: boolean, drop: string[] = []) =>
    onChange(
      on
        ? [...selected.filter((other) => !drop.includes(other.id)), place]
        : selected.filter((other) => other.id !== place.id),
    );
  const shown = (workspaces.data ?? []).filter((workspace) => workspace.kind === 'workspace');
  return (
    <div className="flex flex-col gap-2">
      <Input
        aria-label={t('assistants.filterPlaces')}
        placeholder={t('assistants.filterPlaceholder')}
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      <div className="flex max-h-72 flex-col gap-1 overflow-auto rounded-md border p-2">
        {shown.map((workspace) => (
          <WorkspacePlaces
            key={workspace.id}
            workspace={workspace}
            filter={filter}
            selected={selected.map((place) => place.id)}
            onToggle={toggle}
          />
        ))}
        {workspaces.isFetched && shown.length === 0 && <p className="text-sm text-muted">{t('assistants.noWorkspaces')}</p>}
      </div>
      <p className="text-xs text-muted">
        {t('assistants.selected', { count: selected.length })}
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
  onToggle: (place: Place, on: boolean, drop?: string[]) => void;
}) {
  const { t } = useTranslation('settings');
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
          aria-label={t(open ? 'assistants.hideLists' : 'assistants.showLists', { workspace: workspace.name })}
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
                { id: workspace.id, name: workspace.name, kind: workspace.kind },
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
                onChange={(event) =>
                  onToggle(
                    { id: collection.id, name: collection.name, kind: collection.kind, workspace: workspace.name },
                    event.target.checked,
                  )
                }
              />
              {collection.name}
              <span className="text-xs text-muted">{collection.kind === 'library' ? t('assistants.library') : t('assistants.list')}</span>
            </label>
          ))}
          {collections.isFetched && matching.length === 0 && (
            <p className="text-xs text-muted">{t('assistants.noLists')}</p>
          )}
        </div>
      )}
    </div>
  );
}
