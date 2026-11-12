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
  /** The glyph of the app's icon; empty when the icon shows the name's initials. */
  icon: string;
  /** The icon's color as #rrggbb; empty when the name chooses it. */
  color: string;
  state: AppState;
  parameters: Record<string, unknown>;
  /** The current version's form; lists of apps leave it out. */
  parameters_schema?: Record<string, unknown>;
  current_version: number;
  source?: string;
  /** Set in the launcher's list. */
  workspace_name?: string;
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
  /** Every list of apps: a workspace's and the launcher's. */
  lists: ['apps'] as const,
  list: (workspaceId: string) => ['apps', workspaceId] as const,
  launcher: ['apps', 'launcher'] as const,
  one: (id: string) => ['app', id] as const,
};

export const appsQuery = (workspaceId: string) =>
  queryOptions({
    queryKey: appKeys.list(workspaceId),
    queryFn: async () => (await api<{ value: App[] }>(`/v1/workspaces/${workspaceId}/apps`)).data.value,
  });

/** The apps the viewer may see in every workspace, by name, with their workspace's name. */
export const launcherQuery = queryOptions({
  queryKey: appKeys.launcher,
  queryFn: async () => (await api<{ value: App[] }>('/v1/apps')).data.value,
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
