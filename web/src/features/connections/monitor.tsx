import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { api } from '@/api/http';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Field, QueryFeedback } from '@/features/admin/common';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { subscribeStream } from '@/lib/live';
import { callsQuery, type Connection } from './api';
import { channelUses } from './editor';

interface Heard {
  topic: string;
  payload: unknown;
  encoding?: string;
  retained?: boolean;
  at: string;
}

const shown = 100;

/** A value typed as JSON when it reads as JSON, otherwise as text. */
function typed(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/** A channel's live messages, newest first; a new channel starts a new list. */
function ChannelMessages({ connection, channel }: { connection: Connection; channel: string }) {
  const { t } = useTranslation('connections');
  const [heard, setHeard] = useState<Heard[]>([]);
  const [ended, setEnded] = useState<string>();
  useEffect(
    () =>
      subscribeStream(
        { source: 'connections', workspace: connection.workspace_id, key: connection.key, channel },
        {
          frame: (event, data) => {
            if (event !== 'connection.messages') return;
            setHeard((current) => [...[...((data.messages as Heard[]) ?? [])].reverse(), ...current].slice(0, shown));
          },
          ended: (end) => setEnded(end.detail ?? t(`monitor.ended.${end.reason}`)),
        },
      ),
    [channel, connection.workspace_id, connection.key, t],
  );
  return (
    <>
      {ended && <Alert tone="warning">{ended}</Alert>}
      {heard.length === 0 && !ended && <p className="text-sm text-muted">{t('monitor.waiting')}</p>}
      <ul className="grid max-h-96 gap-1 overflow-auto font-mono text-xs">
        {heard.map((m, i) => (
          <li key={`${m.at}-${m.topic}-${i}`} className="grid gap-0.5 rounded border p-2">
            <span className="flex flex-wrap items-center gap-2 text-muted">
              {new Date(m.at).toLocaleTimeString()} <span className="text-foreground">{m.topic}</span>
              {m.retained && <Badge>{t('monitor.retained')}</Badge>}
            </span>
            <span className="break-all">{m.encoding === 'base64' ? t('monitor.binary') : typeof m.payload === 'string' ? m.payload : JSON.stringify(m.payload)}</span>
          </li>
        ))}
      </ul>
    </>
  );
}

function MQTTMonitor({ connection }: { connection: Connection }) {
  const { t } = useTranslation('connections');
  const seeing = connection.channels.filter((c) => channelUses('mqtt', c).includes('see'));
  const controlling = connection.channels.filter((c) => channelUses('mqtt', c).includes('control'));
  const [channel, setChannel] = useState(seeing[0]?.key ?? '');
  const [target, setTarget] = useState(controlling[0]?.key ?? '');
  const [topic, setTopic] = useState('');
  const [payload, setPayload] = useState('');
  const publish = useMutation({
    mutationFn: async () => (await api(`/v1/workspaces/${connection.workspace_id}/connections/${connection.key}/channels/${target}/publish`, { method: 'POST', body: { topic, payload: typed(payload) } })).data,
  });
  return (
    <div className="grid gap-4">
      {seeing.length === 0 ? (
        <p className="text-sm text-muted">{t('monitor.noSee')}</p>
      ) : (
        <section className="grid gap-2">
          <Field id="monitor-channel" label={t('monitor.channel')}>
            <Select id="monitor-channel" value={channel} onChange={(e) => setChannel(e.target.value)}>
              {seeing.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.name || c.key} · {c.topics}
                </option>
              ))}
            </Select>
          </Field>
          {channel && <ChannelMessages key={channel} connection={connection} channel={channel} />}
        </section>
      )}
      {controlling.length > 0 && (
        <form
          className="grid gap-2 border-t pt-4"
          onSubmit={(event) => {
            event.preventDefault();
            publish.mutate();
          }}
        >
          <h3 className="text-sm font-semibold">{t('monitor.publish')}</h3>
          <div className="grid gap-2 sm:grid-cols-2">
            <Select aria-label={t('monitor.channel')} value={target} onChange={(e) => setTarget(e.target.value)}>
              {controlling.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.name || c.key} · {c.topics}
                </option>
              ))}
            </Select>
            <Input aria-label={t('monitor.topic')} placeholder={t('monitor.topic')} required className="font-mono" value={topic} onChange={(e) => setTopic(e.target.value)} />
          </div>
          <Textarea aria-label={t('monitor.payload')} placeholder={t('monitor.payloadHint')} className="font-mono text-xs" rows={2} value={payload} onChange={(e) => setPayload(e.target.value)} />
          <div className="flex items-center gap-2">
            <Button type="submit" variant="primary" disabled={publish.isPending}>
              {t('monitor.send')}
            </Button>
            {publish.isSuccess && <span className="text-xs text-muted">{t('monitor.sent')}</span>}
          </div>
          {publish.error && <Alert>{problemMessage(publish.error)}</Alert>}
        </form>
      )}
    </div>
  );
}

function HTTPMonitor({ connection }: { connection: Connection }) {
  const { t } = useTranslation('connections');
  const calls = useQuery(callsQuery(connection.id));
  const [channel, setChannel] = useState(connection.channels[0]?.key ?? '');
  const methods = connection.channels.find((c) => c.key === channel)?.methods ?? ['GET'];
  const [method, setMethod] = useState(methods[0] ?? 'GET');
  const [path, setPath] = useState('/');
  const [body, setBody] = useState('');
  const call = useMutation({
    mutationFn: async () =>
      (await api<{ status: number; body?: unknown; body_base64?: string }>(`/v1/workspaces/${connection.workspace_id}/connections/${connection.key}/channels/${channel}/call`, { method: 'POST', body: { method, path, ...(body && method !== 'GET' && method !== 'HEAD' ? { body: typed(body) } : {}) } })).data,
    onSettled: () => void calls.refetch(),
  });
  return (
    <div className="grid gap-4">
      <form
        className="grid gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          call.mutate();
        }}
      >
        <h3 className="text-sm font-semibold">{t('monitor.tryCall')}</h3>
        <div className="grid gap-2 sm:grid-cols-[1fr_auto_2fr]">
          <Select
            aria-label={t('monitor.channel')}
            value={channel}
            onChange={(e) => {
              // A channel allows its own methods: start from its first.
              setChannel(e.target.value);
              setMethod(connection.channels.find((c) => c.key === e.target.value)?.methods?.[0] ?? 'GET');
            }}
          >
            {connection.channels.map((c) => (
              <option key={c.key} value={c.key}>
                {c.name || c.key} · {c.path}
              </option>
            ))}
          </Select>
          <Select aria-label={t('monitor.method')} value={method} onChange={(e) => setMethod(e.target.value)}>
            {methods.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </Select>
          <Input aria-label={t('monitor.path')} required className="font-mono" value={path} onChange={(e) => setPath(e.target.value)} />
        </div>
        {method !== 'GET' && method !== 'HEAD' && <Textarea aria-label={t('monitor.body')} placeholder={t('monitor.payloadHint')} className="font-mono text-xs" rows={2} value={body} onChange={(e) => setBody(e.target.value)} />}
        <div>
          <Button type="submit" variant="primary" disabled={call.isPending || !channel}>
            {t('monitor.send')}
          </Button>
        </div>
        {call.error && <Alert>{problemMessage(call.error)}</Alert>}
        {call.data && (
          <pre className="max-h-48 overflow-auto rounded border p-2 text-xs">
            {call.data.status} {call.data.body_base64 ? t('monitor.binary') : JSON.stringify(call.data.body, null, 2)}
          </pre>
        )}
      </form>
      <section className="grid gap-2 border-t pt-4">
        <h3 className="text-sm font-semibold">{t('monitor.calls')}</h3>
        <QueryFeedback query={calls} />
        {calls.data?.length === 0 && <p className="text-sm text-muted">{t('monitor.noCalls')}</p>}
        <table className="w-full text-left text-xs">
          <tbody>
            {(calls.data ?? []).map((c, i) => (
              <tr key={`${c.at}-${i}`} className="border-t">
                <td className="py-1 pr-2 text-muted">{new Date(c.at).toLocaleTimeString()}</td>
                <td className="pr-2 font-mono">
                  {c.method} {c.path}
                </td>
                <td className="pr-2">{c.error ? <Badge tone="danger">{c.status ?? t('monitor.failed')}</Badge> : <Badge tone={c.status && c.status < 400 ? 'success' : 'warning'}>{c.status}</Badge>}</td>
                <td className="pr-2 text-muted">{c.cached ? t('monitor.cached') : `${c.took_ms} ms`}</td>
                <td className="text-muted">{c.caller}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}

/** Watches a connection: an MQTT channel's live messages with a publish box, or an HTTP connection's last calls with a box to try one. */
export function ConnectionMonitor({ connection, onClose }: { connection: Connection; onClose: () => void }) {
  const { t } = useTranslation('connections');
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(800px,96vw)]">
        <header className="flex items-center gap-2 border-b p-4">
          <div className="flex-1">
            <SheetTitle>{t('monitor.title', { name: connection.name })}</SheetTitle>
            <SheetDescription>{t('monitor.description')}</SheetDescription>
          </div>
          <SheetClose />
        </header>
        <div className="min-h-0 flex-1 overflow-auto p-4">{connection.kind === 'mqtt' ? <MQTTMonitor connection={connection} /> : <HTTPMonitor connection={connection} />}</div>
      </SheetContent>
    </Sheet>
  );
}
