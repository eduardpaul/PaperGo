import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { childrenQuery } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Field, QueryFeedback } from '@/features/admin/common';
import { useTranslation } from '@/i18n';
import { connectionKeys, type Access, type Channel, type Connection, type ConnectionKind, type ConnectionSave, type ConnectionUse, type Credentials } from './api';

const methods = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE'];
const allUses: ConnectionUse[] = ['see', 'control'];
const defaultAccess: Access = { level: 'write', on: 'workspace' };

/** The uses a channel serves: MQTT's chosen uses, HTTP's from its methods. */
export function channelUses(kind: ConnectionKind, channel: Channel): ConnectionUse[] {
  if (kind === 'mqtt') return channel.uses ?? [];
  const out: ConnectionUse[] = [];
  if ((channel.methods ?? []).some((m) => m === 'GET' || m === 'HEAD')) out.push('see');
  if ((channel.methods ?? []).some((m) => m !== 'GET' && m !== 'HEAD')) out.push('control');
  return out;
}

/** A channel without what its uses no longer need: access for a use it dropped, retain without control, cache without GET. */
export function settledChannel(kind: ConnectionKind, channel: Channel): Channel {
  const uses = channelUses(kind, channel);
  const access = Object.fromEntries(Object.entries(channel.access ?? {}).filter(([use]) => uses.includes(use as ConnectionUse)));
  const out: Channel = { ...channel, access };
  if (kind === 'mqtt' && !uses.includes('control')) delete out.retain;
  if (kind === 'http' && !(channel.methods ?? []).includes('GET')) delete out.cache;
  return out;
}

function headerLines(headers: Record<string, string> | undefined) {
  return Object.entries(headers ?? {})
    .map(([name, value]) => `${name}: ${value}`)
    .join('\n');
}

function parseHeaders(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const at = line.indexOf(':');
    if (at > 0) out[line.slice(0, at).trim()] = line.slice(at + 1).trim();
  }
  return out;
}

/** Chooses who may use a channel one way: a level on the workspace or one of its lists. */
function AccessPicker({ id, use, value, lists, onChange }: { id: string; use: ConnectionUse; value: Access; lists: { id: string; name: string }[]; onChange: (access: Access) => void }) {
  const { t } = useTranslation('connections');
  return (
    <div className="grid gap-1">
      <Label htmlFor={`${id}-level`}>{t(use === 'see' ? 'access.whoSees' : 'access.whoControls')}</Label>
      <div className="flex flex-wrap gap-2">
        <Select id={`${id}-level`} value={value.level} onChange={(e) => onChange({ ...value, level: e.target.value as Access['level'] })} aria-label={t('access.level')}>
          <option value="read">{t('access.read')}</option>
          <option value="write">{t('access.write')}</option>
          <option value="manage">{t('access.manage')}</option>
        </Select>
        <Select value={value.on} onChange={(e) => onChange({ ...value, on: e.target.value })} aria-label={t('access.on')}>
          <option value="workspace">{t('access.workspace')}</option>
          {lists.map((list) => (
            <option key={list.id} value={list.id}>
              {list.name}
            </option>
          ))}
          {value.on !== 'workspace' && !lists.some((list) => list.id === value.on) && <option value={value.on}>{t('access.missingList')}</option>}
        </Select>
      </div>
    </div>
  );
}

function ChannelEditor({ kind, channel, index, lists, onChange, onRemove }: { kind: ConnectionKind; channel: Channel; index: number; lists: { id: string; name: string }[]; onChange: (channel: Channel) => void; onRemove: () => void }) {
  const { t } = useTranslation(['connections', 'common']);
  const id = `channel-${index}`;
  const uses = channelUses(kind, channel);
  const toggle = <T extends string>(values: T[] | undefined, value: T, on: boolean) => (on ? [...(values ?? []), value] : (values ?? []).filter((v) => v !== value));
  return (
    <fieldset className="grid gap-3 rounded-lg border p-3">
      <legend className="px-1 text-sm font-medium">{channel.name || channel.key || t('channels.new')}</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field id={`${id}-key`} label={t('channels.key')} hint={t('channels.keyHint')}>
          <Input id={`${id}-key`} required pattern="[a-z0-9][a-z0-9_-]*" maxLength={64} value={channel.key} onChange={(e) => onChange({ ...channel, key: e.target.value })} />
        </Field>
        <Field id={`${id}-name`} label={t('common:fields.name')}>
          <Input id={`${id}-name`} maxLength={200} value={channel.name ?? ''} onChange={(e) => onChange({ ...channel, name: e.target.value })} />
        </Field>
      </div>
      {kind === 'mqtt' ? (
        <>
          <Field id={`${id}-topics`} label={t('channels.topics')} hint={t('channels.topicsHint')}>
            <Input id={`${id}-topics`} required className="font-mono" value={channel.topics ?? ''} onChange={(e) => onChange({ ...channel, topics: e.target.value })} />
          </Field>
          <div className="flex flex-wrap gap-4 text-sm">
            {allUses.map((use) => (
              <label key={use} className="flex items-center gap-2">
                <input type="checkbox" checked={uses.includes(use)} onChange={(e) => onChange({ ...channel, uses: toggle(channel.uses, use, e.target.checked) })} />
                {t(`uses.${use}`)}
              </label>
            ))}
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={!!channel.retain} disabled={!uses.includes('control')} onChange={(e) => onChange({ ...channel, retain: e.target.checked })} />
              {t('channels.retain')}
            </label>
          </div>
        </>
      ) : (
        <>
          <Field id={`${id}-path`} label={t('channels.path')} hint={t('channels.pathHint')}>
            <Input id={`${id}-path`} required className="font-mono" value={channel.path ?? ''} onChange={(e) => onChange({ ...channel, path: e.target.value })} />
          </Field>
          <div className="flex flex-wrap gap-3 text-sm" role="group" aria-label={t('channels.methods')}>
            {methods.map((method) => (
              <label key={method} className="flex items-center gap-1">
                <input type="checkbox" checked={(channel.methods ?? []).includes(method)} onChange={(e) => onChange({ ...channel, methods: toggle(channel.methods, method, e.target.checked) })} />
                {method}
              </label>
            ))}
          </div>
          <Field id={`${id}-cache`} label={t('channels.cache')} hint={t('channels.cacheHint')}>
            <Input id={`${id}-cache`} value={channel.cache ?? ''} disabled={!(channel.methods ?? []).includes('GET')} onChange={(e) => onChange({ ...channel, cache: e.target.value })} />
          </Field>
        </>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        {uses.map((use) => (
          <AccessPicker key={use} id={`${id}-${use}`} use={use} lists={lists} value={channel.access?.[use] ?? defaultAccess} onChange={(access) => onChange({ ...channel, access: { ...channel.access, [use]: access } })} />
        ))}
      </div>
      <div>
        <Button size="sm" variant="danger" onClick={onRemove}>
          <Trash2 /> {t('channels.remove')}
        </Button>
      </div>
    </fieldset>
  );
}

/**
 * Adds or edits a connection: its target, TLS and credentials, kept
 * encrypted and never shown again, and its channels with who may see and
 * control each.
 */
export function ConnectionEditor({ workspaceId, connection, onClose, onSaved }: { workspaceId: string; connection?: Connection; onClose: () => void; onSaved: (saved: Connection) => void }) {
  const { t } = useTranslation(['connections', 'common']);
  const client = useQueryClient();
  const children = useQuery(childrenQuery(workspaceId));
  const lists = (children.data?.value ?? []).filter((r) => r.kind === 'list' || r.kind === 'library').map((r) => ({ id: r.id, name: r.name }));
  const [kind, setKind] = useState<ConnectionKind>(connection?.kind ?? 'mqtt');
  const [name, setName] = useState(connection?.name ?? '');
  const [key, setKey] = useState('');
  const [enabled, setEnabled] = useState(connection?.enabled ?? true);
  const [target, setTarget] = useState(connection?.target ?? '');
  const [ca, setCA] = useState(connection?.tls.ca ?? '');
  const [credentials, setCredentials] = useState<Credentials>(connection ? { ...connection.credentials } : { type: 'none' });
  const [secretsChanged, setSecretsChanged] = useState(!connection);
  const [headers, setHeaders] = useState(headerLines(connection?.headers));
  const [timeout, setTimeoutText] = useState(connection?.timeout ?? '10s');
  const [sign, setSign] = useState(connection?.sign ?? false);
  const [healthPath, setHealthPath] = useState(connection?.health_path ?? '');
  const [channels, setChannels] = useState<Channel[]>(connection?.channels ?? []);
  const credential = (patch: Credentials) => {
    setCredentials((current) => ({ ...current, ...patch }));
    setSecretsChanged(true);
  };
  const save = useMutation({
    mutationFn: async () => {
      const body: ConnectionSave = { name: name.trim(), enabled, target: target.trim(), tls: { ca }, channels: channels.map((c) => settledChannel(kind, c)), ...(secretsChanged ? { credentials } : {}) };
      if (kind === 'http') Object.assign(body, { headers: parseHeaders(headers), timeout, sign, health_path: healthPath || undefined });
      if (!connection) Object.assign(body, { workspace_id: workspaceId, kind, ...(key ? { key } : {}) });
      return (await api<Connection>(connection ? `/v1/connections/${connection.id}` : '/v1/connections', { method: connection ? 'PUT' : 'POST', ifMatch: connection?.version, body })).data;
    },
    onSuccess: async (saved) => {
      await client.invalidateQueries({ queryKey: connectionKeys.list(workspaceId) });
      onSaved(saved);
    },
  });
  const type = credentials.type ?? 'none';
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(900px,96vw)]">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <header className="flex items-center gap-2 border-b p-4">
            <div className="flex-1">
              <SheetTitle>{connection ? t('editor.title', { name: connection.name }) : t('editor.newTitle')}</SheetTitle>
              <SheetDescription>{t('editor.description')}</SheetDescription>
            </div>
            <SheetClose />
          </header>
          <div className="grid min-h-0 flex-1 content-start gap-4 overflow-auto p-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <Field id="connection-name" label={t('common:fields.name')}>
                <Input id="connection-name" required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} />
              </Field>
              {connection ? (
                <Field id="connection-key" label={t('editor.key')} hint={t('editor.keyFixed')}>
                  <Input id="connection-key" readOnly className="font-mono" value={connection.key} />
                </Field>
              ) : (
                <Field id="connection-key" label={t('editor.key')} hint={t('editor.keyHint')}>
                  <Input id="connection-key" className="font-mono" pattern="[a-z0-9][a-z0-9_-]*" maxLength={100} value={key} onChange={(e) => setKey(e.target.value)} />
                </Field>
              )}
              <Field id="connection-kind" label={t('editor.kind')}>
                <Select
                  id="connection-kind"
                  value={kind}
                  disabled={!!connection}
                  onChange={(e) => {
                    // Channels and credentials belong to their kind: the other kind starts without them.
                    const next = e.target.value as ConnectionKind;
                    setKind(next);
                    setChannels([]);
                    if (next === 'mqtt' && (type === 'bearer' || type === 'header')) setCredentials({ type: 'none' });
                  }}
                >
                  <option value="mqtt">{t('kind.mqtt')}</option>
                  <option value="http">{t('kind.http')}</option>
                </Select>
              </Field>
              <label className="flex items-center gap-2 self-end pb-2 text-sm">
                <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
                {t('editor.enabled')}
              </label>
            </div>
            <Field id="connection-target" label={t('editor.target')} hint={t(kind === 'mqtt' ? 'editor.targetHintMqtt' : 'editor.targetHintHttp')}>
              <Input id="connection-target" required className="font-mono" value={target} onChange={(e) => setTarget(e.target.value)} />
            </Field>
            <fieldset className="grid gap-3">
              <legend className="mb-1 text-sm font-medium">{t('editor.credentials')}</legend>
              {connection && !secretsChanged && <p className="text-xs text-muted">{t('editor.credentialsKept')}</p>}
              <Select aria-label={t('editor.credentialType')} value={type} onChange={(e) => credential({ type: e.target.value as Credentials['type'] })}>
                <option value="none">{t('credentials.none')}</option>
                <option value="basic">{t('credentials.basic')}</option>
                {kind === 'http' && <option value="bearer">{t('credentials.bearer')}</option>}
                {kind === 'http' && <option value="header">{t('credentials.header')}</option>}
              </Select>
              {type === 'basic' && (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field id="credential-username" label={t('credentials.username')}>
                    <Input id="credential-username" autoComplete="off" value={credentials.username ?? ''} onChange={(e) => credential({ username: e.target.value })} />
                  </Field>
                  <Field id="credential-password" label={t('credentials.password')}>
                    <Input id="credential-password" type="password" autoComplete="new-password" placeholder={connection && !secretsChanged ? t('editor.unchanged') : ''} value={credentials.password ?? ''} onChange={(e) => credential({ password: e.target.value })} />
                  </Field>
                </div>
              )}
              {type === 'bearer' && (
                <Field id="credential-token" label={t('credentials.token')}>
                  <Input id="credential-token" type="password" autoComplete="off" placeholder={connection && !secretsChanged ? t('editor.unchanged') : ''} value={credentials.token ?? ''} onChange={(e) => credential({ token: e.target.value })} />
                </Field>
              )}
              {type === 'header' && (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field id="credential-name" label={t('credentials.headerName')}>
                    <Input id="credential-name" value={credentials.name ?? ''} onChange={(e) => credential({ name: e.target.value })} />
                  </Field>
                  <Field id="credential-value" label={t('credentials.headerValue')}>
                    <Input id="credential-value" type="password" autoComplete="off" placeholder={connection && !secretsChanged ? t('editor.unchanged') : ''} value={credentials.value ?? ''} onChange={(e) => credential({ value: e.target.value })} />
                  </Field>
                </div>
              )}
            </fieldset>
            <Field id="connection-ca" label={t('editor.ca')} hint={t('editor.caHint')}>
              <Textarea id="connection-ca" className="font-mono text-xs" rows={3} value={ca} onChange={(e) => setCA(e.target.value)} />
            </Field>
            {kind === 'http' && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field id="connection-headers" label={t('editor.headers')} hint={t('editor.headersHint')}>
                  <Textarea id="connection-headers" className="font-mono text-xs" rows={3} value={headers} onChange={(e) => setHeaders(e.target.value)} />
                </Field>
                <div className="grid content-start gap-3">
                  <Field id="connection-timeout" label={t('editor.timeout')}>
                    <Input id="connection-timeout" value={timeout} onChange={(e) => setTimeoutText(e.target.value)} />
                  </Field>
                  <Field id="connection-health" label={t('editor.healthPath')}>
                    <Input id="connection-health" className="font-mono" placeholder="/" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} />
                  </Field>
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={sign} onChange={(e) => setSign(e.target.checked)} />
                    {t('editor.sign')}
                  </label>
                </div>
              </div>
            )}
            <section className="grid gap-3">
              <div className="flex items-center gap-2">
                <h3 className="flex-1 text-sm font-semibold">{t('channels.title')}</h3>
                <Button size="sm" onClick={() => setChannels([...channels, kind === 'mqtt' ? { key: '', topics: '', uses: ['see'] } : { key: '', path: '/', methods: ['GET'] }])}>
                  <Plus /> {t('channels.add')}
                </Button>
              </div>
              <p className="text-xs text-muted">{t('channels.description')}</p>
              {channels.map((channel, index) => (
                <ChannelEditor
                  key={index}
                  kind={kind}
                  channel={channel}
                  index={index}
                  lists={lists}
                  onChange={(next) => setChannels(channels.map((c, i) => (i === index ? next : c)))}
                  onRemove={() => setChannels(channels.filter((_, i) => i !== index))}
                />
              ))}
              <p className="text-xs text-muted">{t('access.owners')}</p>
            </section>
            <QueryFeedback query={{ isPending: false, error: save.error }} />
          </div>
          <footer className="flex flex-wrap justify-end gap-2 border-t p-4">
            <Button onClick={onClose}>{t('common:actions.close')}</Button>
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {t('common:actions.save')}
            </Button>
          </footer>
        </form>
      </SheetContent>
    </Sheet>
  );
}
