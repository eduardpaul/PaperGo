import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query';
import { api, continuation, isApiError, listOf } from './http';
import type {
  Task,
  BuiltInState,
  CollectionTemplate,
  ContentType,
  FieldDefinition,
  Home,
  Inbox,
  ListView,
  Me,
  Page,
  Permissions,
  Preferences,
  Relationship,
  Resource,
  AccessToken,
  Revision,
  Scope,
  SmartFolder,
  StagedFile,
  Term,
  UserProfile,
  Workflow,
  WorkflowRun,
  WorkspaceMember,
} from './types';

export const keys = {
  me: ['me'] as const,
  preferences: ['preferences'] as const,
  orgPreferences: ['organization', 'preferences'] as const,
  home: ['home'] as const,
  inboxes: ['inboxes'] as const,
  workspaces: ['workspaces'] as const,
  resources: ['resources'] as const,
  resource: (id: string) => ['resources', id] as const,
  children: (id: string, query: object) => ['resources', id, 'children', query] as const,
  fields: (id: string) => ['resources', id, 'fields'] as const,
  contentTypes: (id: string) => ['resources', id, 'content-types'] as const,
  views: (id: string) => ['resources', id, 'views'] as const,
  smartFolders: ['smart-folders'] as const,
  templates: ['collection-templates'] as const,
  people: ['people'] as const,
  users: ['users'] as const,
  tasks: (filter: TaskFilter) => ['tasks', filter] as const,
  task: (id: string) => ['tasks', 'one', id] as const,
  stagedFile: (id: string) => ['staged-files', id] as const,
};

/** Which of the caller's tasks to list: waiting for them, or by status. */
export interface TaskFilter {
  waiting?: boolean;
  status?: string;
  kind?: string;
}

export const myTasksQuery = (filter: TaskFilter) =>
  infiniteQueryOptions({
    queryKey: keys.tasks(filter),
    initialPageParam: '/v1/me/tasks',
    queryFn: async ({ pageParam }) =>
      (
        await api<Page<Task>>(pageParam, {
          query: pageParam === '/v1/me/tasks'
            ? { waiting: filter.waiting ? 'true' : undefined, status: filter.status, kind: filter.kind }
            : undefined,
        })
      ).data,
    getNextPageParam: (page) => continuation(page['@odata.nextLink']) ?? undefined,
  });

export const taskQuery = (id: string) =>
  queryOptions({
    queryKey: keys.task(id),
    queryFn: async () => (await api<Task>(`/v1/tasks/${id}`)).data,
  });

export const stagedFileQuery = (id: string) =>
  queryOptions({
    queryKey: keys.stagedFile(id),
    queryFn: async () => (await api<StagedFile>(`/v1/staged-files/${id}`)).data,
  });

export const meQuery = queryOptions({
  queryKey: keys.me,
  queryFn: async () => (await api<Me>('/v1/identity/me')).data,
  staleTime: 5 * 60_000,
});

export const preferencesQuery = queryOptions({
  queryKey: keys.preferences,
  queryFn: async () => (await api<Preferences>('/v1/identity/me/preferences')).data,
  staleTime: 5 * 60_000,
});

export const organizationPreferencesQuery = queryOptions({
  queryKey: keys.orgPreferences,
  queryFn: async () => (await api<Preferences>('/v1/organization/preferences')).data,
  staleTime: 5 * 60_000,
});

export const homeQuery = queryOptions({
  queryKey: keys.home,
  queryFn: async () => (await api<Home>('/v1/identity/me/home')).data,
});

export const inboxesQuery = queryOptions({
  queryKey: keys.inboxes,
  queryFn: async () => listOf(await api<Inbox[] | Page<Inbox>>('/v1/identity/me/inboxes').then((r) => r.data)),
});

export const workspacesQuery = queryOptions({
  queryKey: keys.workspaces,
  queryFn: async () => listOf((await api<Page<Resource>>('/v1/workspaces', { query: { $top: 200 } })).data),
});

export const resourceQuery = (id: string) =>
  queryOptions({
    queryKey: keys.resource(id),
    queryFn: async () => (await api<Resource>(`/v1/resources/${id}`)).data,
  });

export const childrenQuery = (id: string, query: { q?: string; parent?: boolean } = {}) =>
  queryOptions({
    queryKey: keys.children(id, query),
    queryFn: async () => (await api<Page<Resource>>(`/v1/resources/${id}/children`, { query: { q: query.q, $top: 50 } })).data,
  });

export const fieldsQuery = (id: string) =>
  queryOptions({
    queryKey: keys.fields(id),
    queryFn: async () => listOf((await api<Page<FieldDefinition>>(`/v1/resources/${id}/fields`)).data),
  });

export const contentTypesQuery = (id: string) =>
  queryOptions({
    queryKey: keys.contentTypes(id),
    queryFn: async () => listOf((await api<Page<ContentType> | ContentType[]>(`/v1/resources/${id}/content-types`)).data),
  });

export const viewsQuery = (id: string) =>
  queryOptions({
    queryKey: keys.views(id),
    queryFn: async () => listOf((await api<Page<ListView>>(`/v1/resources/${id}/views`, { query: { $top: 100 } })).data),
  });

export const templatesQuery = queryOptions({
  queryKey: keys.templates,
  queryFn: async () => listOf((await api<Page<CollectionTemplate> | CollectionTemplate[]>('/v1/collection-templates')).data),
});

export const smartFoldersQuery = queryOptions({
  queryKey: keys.smartFolders,
  queryFn: async () => listOf((await api<Page<SmartFolder>>('/v1/smart-folders', { query: { $top: 200 } })).data),
});

export const peopleQuery = queryOptions({
  queryKey: keys.people,
  queryFn: async () => listOf((await api<string[] | Page<string>>('/v1/people')).data),
});

export const usersQuery = queryOptions({
  queryKey: keys.users,
  queryFn: async () => {
    try {
      return listOf((await api<UserProfile[] | Page<UserProfile>>('/v1/identity/users')).data);
    } catch (error) {
      if (isApiError(error, 403)) return [];
      throw error;
    }
  },
});

export async function collectionChildren(workspaceId: string) {
  return listOf((await api<Page<Resource>>(`/v1/resources/${workspaceId}/children`, { query: { $top: 200 } })).data).filter(
    (resource) => resource.kind === 'list' || resource.kind === 'library',
  );
}

export async function members(workspaceId: string) {
  return listOf((await api<Page<WorkspaceMember>>(`/v1/workspaces/${workspaceId}/members`)).data);
}

export async function workflows(workspaceId: string) {
  return listOf((await api<Page<Workflow>>(`/v1/workspaces/${workspaceId}/workflows`)).data);
}

export async function builtIns(workspaceId: string) {
  return listOf((await api<Page<BuiltInState> | BuiltInState[]>(`/v1/workspaces/${workspaceId}/workflow-builtins`)).data);
}

export async function workflowRuns(workspaceId: string) {
  return (await api<Page<WorkflowRun>>(`/v1/workspaces/${workspaceId}/workflow-runs`, { query: { $top: 50 } })).data;
}

export async function revisions(itemId: string) {
  return (await api<Page<Revision>>(`/v1/items/${itemId}/revisions`)).data;
}

export async function relationships(itemId: string) {
  return (await api<Page<Relationship>>(`/v1/items/${itemId}/relationships`)).data;
}

export async function permissions(id: string) {
  return (await api<Permissions>(`/v1/resources/${id}/permissions`)).data;
}

export async function scopes() {
  return (await api<string[]>('/v1/identity/me/scopes')).data;
}

export async function scopeCatalog() {
  try {
    return (await api<Scope[]>('/v1/identity/scopes')).data;
  } catch (error) {
    if (isApiError(error, 403)) return [];
    throw error;
  }
}

export async function tokens() {
  return listOf((await api<Page<AccessToken> | AccessToken[]>('/v1/identity/me/tokens')).data);
}

export async function smartFolder(id: string) {
  return (await api<SmartFolder>(`/v1/smart-folders/${id}`)).data;
}

export async function terms(setId: string, q: string) {
  return listOf((await api<Page<Term>>(`/v1/term-sets/${setId}/terms`, { query: { q, $top: 20 } })).data);
}

export async function termsById(ids: string[]) {
  if (ids.length === 0) return [];
  return listOf((await api<Page<Term>>('/v1/terms', { query: { ids: ids.join(',') } })).data);
}

export async function keywords(q: string) {
  return listOf((await api<Page<Term>>('/v1/keywords', { query: { q, $top: 20 } })).data);
}

export async function countChildren(id: string) {
  const page = (
    await api<Page<Resource>>(`/v1/resources/${id}/query`, {
      method: 'POST',
      query: { $top: 1 },
      body: { include_total: true, query: {} },
    })
  ).data;
  return page.total ?? listOf(page).length;
}
