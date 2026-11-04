import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useEffect, useMemo, useRef, useState } from 'react';
import { api } from '@/api/http';
import { meQuery, preferencesQuery, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { can } from '@/features/lists/access';
import { useTranslation } from '@/i18n';
import { onItemChange } from '@/lib/live';
import { appBundle, type App } from './api';
import { appDocument, send, type BridgeRequest } from './bridge';

type Target = { itemId: string } | { taskId: string } | { listId: string };

type FrameMessage = ({ type: 'papergo:request'; id: number } & BridgeRequest) | { type: 'papergo:open'; target: Target };

const runtimeQuery = { queryKey: ['app-runtime'], queryFn: async () => (await import('@sdk/app-runtime.js?raw')).default, staleTime: Infinity };

/**
 * Runs an app in a sandboxed frame: no origin, network or navigation of its
 * own, under a CSP that allows only the scripts the page writes into it. The
 * runtime asks for a MessageChannel port before the app's code runs; the
 * page answers that first request only and serves the app's API requests
 * over the port, as the viewer. A frame that loads another document is
 * stopped.
 */
export function AppFrame({ app }: { app: App }) {
  const { t } = useTranslation('apps');
  const navigate = useNavigate();
  const me = useQuery(meQuery);
  const preferences = useQuery(preferencesQuery);
  const workspace = useQuery(resourceQuery(app.workspace_id));
  const runtime = useQuery(runtimeQuery);
  const bundle = useQuery({ queryKey: ['app-bundle', app.id, app.current_version], queryFn: ({ signal }) => appBundle(app.id, signal), staleTime: Infinity });
  const frame = useRef<HTMLIFrameElement>(null);
  const [stopped, setStopped] = useState(false);

  // The start is built from plain values, so a refetch that changes nothing
  // the app is told does not restart it and lose what the viewer was doing.
  const p = preferences.data;
  const init =
    me.data && p && workspace.data
      ? JSON.stringify({
          app: { id: app.id, key: app.key, name: app.name, version: app.current_version },
          params: app.parameters,
          user: { subject: me.data.subject, name: me.data.display_name, preferences: { language: p.language, time_zone: p.time_zone, date_format: p.date_format, time_format: p.time_format, number_format: p.number_format } },
          workspace: { id: workspace.data.id, name: workspace.data.name, role: can(workspace.data, 'manage') ? 'owner' : can(workspace.data, 'write') ? 'member' : 'visitor' },
          theme: window.document.documentElement.classList.contains('dark') ? 'dark' : 'light',
        })
      : undefined;
  const document_ = useMemo(() => (runtime.data && bundle.data && init ? appDocument(runtime.data, bundle.data, JSON.parse(init)) : undefined), [runtime.data, bundle.data, init]);

  useEffect(() => {
    const element = frame.current;
    if (!document_ || !element) return;
    setStopped(false);
    let port: MessagePort | undefined;
    let loads = 0;
    let stopLive: (() => void) | undefined;
    const open = async (target: Target) => {
      if ('taskId' in target) return navigate({ to: '/tasks', search: { id: target.taskId } });
      if ('listId' in target) return navigate({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: app.workspace_id, listId: target.listId } });
      const item = (await api<Resource>(`/v1/resources/${target.itemId}`)).data;
      if (item.container_id) await navigate({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: item.workspace_id, listId: item.container_id }, search: { item: item.id } });
    };
    const serve = async (message: FrameMessage) => {
      if (message.type === 'papergo:request') {
        const response = await send(message);
        port?.postMessage({ type: 'papergo:response', id: message.id, response }, response.bytes ? [response.bytes] : []);
      } else if (message.type === 'papergo:open') {
        void open(message.target).catch(() => undefined);
      }
    };
    const hello = (event: MessageEvent) => {
      if (port || event.source !== element.contentWindow || (event.data as { type?: string } | undefined)?.type !== 'papergo:hello') return;
      const channel = new MessageChannel();
      port = channel.port1;
      port.onmessage = (e: MessageEvent<FrameMessage>) => void serve(e.data);
      element.contentWindow?.postMessage({ type: 'papergo:port' }, '*', [channel.port2]);
      stopLive = onItemChange((change) => port?.postMessage({ type: 'papergo:change', changes: [change] }));
    };
    const stop = () => {
      stopLive?.();
      port?.close();
      port = undefined;
    };
    // The page writes the frame's one document; another load means the
    // app tried to leave it.
    const load = () => {
      loads += 1;
      if (loads > 1) {
        stop();
        setStopped(true);
      }
    };
    window.addEventListener('message', hello);
    element.addEventListener('load', load);
    element.srcdoc = document_;
    return () => {
      window.removeEventListener('message', hello);
      element.removeEventListener('load', load);
      stop();
    };
  }, [document_, app.workspace_id, navigate]);

  const failed = [runtime, bundle, me, preferences, workspace].find((q) => q.isError);
  if (failed) return <Alert tone="danger">{t('frame.notLoaded')}</Alert>;
  if (stopped) return <Alert tone="danger">{t('frame.stopped')}</Alert>;
  return (
    <div className="relative min-h-0 flex-1 overflow-hidden border-t bg-background sm:rounded-lg sm:border">
      {!document_ && <Skeleton className="absolute inset-0" />}
      <iframe ref={frame} title={app.name} sandbox="allow-scripts" className="absolute inset-0 h-full w-full border-0" />
    </div>
  );
}
