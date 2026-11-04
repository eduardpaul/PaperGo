import { queryOptions } from '@tanstack/react-query';
import { api } from '@/api/http';
import { authenticationMode, authorizationHeaders } from '@/api/auth';

export type AppState = 'draft' | 'published';

/** A workspace's app (docs/apps.md). */
export interface App {
  id: string;
  workspace_id: string;
  key: string;
  name: string;
  description: string;
  state: AppState;
  parameters: Record<string, unknown>;
  parameters_schema: Record<string, unknown>;
  current_version: number;
  source?: string;
  can_write: boolean;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface AppVersion {
  number: number;
  source: string;
  created_by: string;
  created_at: string;
}

export const appKeys = {
  list: (workspaceId: string) => ['apps', workspaceId] as const,
  one: (id: string) => ['app', id] as const,
};

export const appsQuery = (workspaceId: string) =>
  queryOptions({
    queryKey: appKeys.list(workspaceId),
    queryFn: async () => (await api<{ value: App[] }>(`/v1/workspaces/${workspaceId}/apps`)).data.value,
  });

export const appQuery = (id: string) =>
  queryOptions({
    queryKey: appKeys.one(id),
    queryFn: async () => (await api<App>(`/v1/apps/${id}`)).data,
  });

/** The compiled code of an app's current version, as text. */
export async function appBundle(id: string, signal?: AbortSignal): Promise<string> {
  const headers: Record<string, string> = { Accept: 'text/javascript' };
  if (authenticationMode() !== 'proxy') Object.assign(headers, authorizationHeaders());
  const response = await fetch(`/v1/apps/${id}/bundle`, { headers, signal, credentials: 'same-origin' });
  if (!response.ok) throw new Error(`the app's code could not be read (${response.status})`);
  return response.text();
}
