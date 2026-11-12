import { queryOptions } from '@tanstack/react-query';
import { api } from '@/api/http';

export type ConnectionKind = 'mqtt' | 'http';
export type ConnectionUse = 'see' | 'control';
export type AccessLevel = 'read' | 'write' | 'manage';

/** Who may use a channel one way: those with level on the workspace or one of its lists. */
export interface Access {
  level: AccessLevel;
  /** workspace, or a list or library ID. */
  on: string;
}

/** A channel (docs/connections.md): MQTT topics with their uses, or HTTP methods on a path. */
export interface Channel {
  key: string;
  name?: string;
  topics?: string;
  uses?: ConnectionUse[];
  retain?: boolean;
  path?: string;
  methods?: string[];
  cache?: string;
  access?: Partial<Record<ConnectionUse, Access>>;
}

export interface Credentials {
  type?: 'none' | 'basic' | 'bearer' | 'header';
  username?: string;
  password?: string;
  token?: string;
  name?: string;
  value?: string;
}

export interface ConnectionStatus {
  state: 'disabled' | 'connecting' | 'connected' | 'failed' | 'ok' | 'failing' | 'unknown';
  last_error?: string;
  connected_at?: string;
  checked_at?: string;
}

/** A connection as its managers see it. */
export interface Connection {
  id: string;
  workspace_id: string;
  key: string;
  name: string;
  kind: ConnectionKind;
  enabled: boolean;
  target: string;
  tls: { ca?: string };
  credentials: Credentials;
  headers?: Record<string, string>;
  timeout?: string;
  sign?: boolean;
  health_path?: string;
  channels: Channel[];
  status: ConnectionStatus;
  /** Shown once, when an HTTP connection is created or given a new one. */
  signing_secret?: string;
  version: number;
}

/** What a create or replace sends. */
export interface ConnectionSave {
  workspace_id?: string;
  key?: string;
  kind?: ConnectionKind;
  name: string;
  enabled: boolean;
  target: string;
  tls?: { ca?: string };
  credentials?: Credentials;
  headers?: Record<string, string>;
  timeout?: string;
  sign?: boolean;
  health_path?: string;
  channels: Channel[];
}

export interface TestResult {
  ok: boolean;
  detail: string;
  took_ms: number;
}

export interface CallRecord {
  at: string;
  channel: string;
  method: string;
  path: string;
  status?: number;
  error?: string;
  took_ms: number;
  caller: string;
  cached?: boolean;
}

export const connectionKeys = {
  list: (workspaceId: string) => ['connections', workspaceId] as const,
  calls: (id: string) => ['connection-calls', id] as const,
};

export const connectionsQuery = (workspaceId: string) =>
  queryOptions({
    queryKey: connectionKeys.list(workspaceId),
    queryFn: async () => (await api<{ value: Connection[] }>('/v1/connections', { query: { workspace: workspaceId } })).data.value,
    // Status changes while the page is open.
    refetchInterval: 10_000,
  });

export const callsQuery = (id: string) =>
  queryOptions({
    queryKey: connectionKeys.calls(id),
    queryFn: async () => (await api<{ value: CallRecord[] }>(`/v1/connections/${id}/calls`)).data.value,
    refetchInterval: 5_000,
  });

/** Whether a connection's state means it works. */
export const working = (status: ConnectionStatus) => status.state === 'connected' || status.state === 'ok' || status.state === 'unknown';
