import { queryOptions } from '@tanstack/react-query';
import { api, isApiError } from '@/api/http';

/** A solution installed in a workspace (docs/solutions.md). */
export interface SolutionSummary {
  key: string;
  name: string;
  version: string;
  paused: boolean;
  applied_by: string;
  applied_at: string;
}

export interface SolutionStatus {
  solution: string;
  version: string;
  ok: boolean;
  problems: string[];
}

export interface SolutionChange {
  action: 'create' | 'update' | 'attach' | 'extend' | 'bind' | 'delete' | 'keep';
  kind: string;
  key: string;
  detail?: string;
}

export interface SolutionRefusal {
  kind: string;
  key: string;
  reason: string;
  /** Edited in PaperGo and changed by the solution: a decision lifts it. */
  conflict?: boolean;
}

export type SlotState = 'create' | 'installed' | 'bound' | 'uses' | 'unresolved';

export interface SolutionSlot {
  key: string;
  kind: 'list' | 'library';
  name?: string;
  owned: boolean;
  uses?: string;
  state: SlotState;
  collection_id?: string;
  collection_name?: string;
}

/** What applying a solution does, or would do. */
export interface SolutionResult {
  solution: string;
  name: string;
  version: string;
  from?: string;
  applied: boolean;
  changes: SolutionChange[];
  refusals: SolutionRefusal[];
  customized: string[];
  warnings: string[];
  slots: SolutionSlot[];
  settings_form?: { properties?: Record<string, SettingSchema> };
  settings: Record<string, unknown>;
}

export interface SettingSchema {
  type?: string;
  title?: string;
  description?: string;
  enum?: unknown[];
}

export type Decision = 'keep' | 'replace';

/** What the person applying a solution chooses. */
export interface SolutionOptions {
  settings?: Record<string, unknown>;
  bindings?: Record<string, string>;
  paused?: boolean;
  prune?: boolean;
  decisions?: Record<string, Decision>;
}

export const solutionKeys = {
  list: (workspaceId: string) => ['solutions', workspaceId] as const,
  status: (workspaceId: string, key: string) => ['solutions', workspaceId, key, 'status'] as const,
};

export const solutionsQuery = (workspaceId: string) =>
  queryOptions({
    queryKey: solutionKeys.list(workspaceId),
    queryFn: async () => (await api<{ value: SolutionSummary[] }>(`/v1/workspaces/${workspaceId}/solutions`)).data.value,
  });

export const solutionStatusQuery = (workspaceId: string, key: string) =>
  queryOptions({
    queryKey: solutionKeys.status(workspaceId, key),
    queryFn: async () =>
      (await api<SolutionStatus>(`/v1/workspaces/${workspaceId}/solutions/${encodeURIComponent(key)}/status`)).data,
  });

/**
 * Plans or applies a packed solution. An apply that is refused answers 409
 * with the same result a plan gives, which is returned, not thrown.
 */
export async function sendSolution(
  workspaceId: string,
  file: Blob,
  step: 'plan' | 'apply',
  options: SolutionOptions,
): Promise<SolutionResult> {
  try {
    const response = await api<SolutionResult>(`/v1/workspaces/${workspaceId}/solutions/${step}`, {
      method: 'POST',
      raw: file,
      headers: { 'Content-Type': 'application/zip' },
      query: { options: JSON.stringify(options) },
    });
    return response.data;
  } catch (error) {
    const refused = isApiError(error, 409) ? (error.problem as unknown as Partial<SolutionResult> | undefined) : undefined;
    if (refused && Array.isArray(refused.refusals) && Array.isArray(refused.changes)) return refused as SolutionResult;
    throw error;
  }
}

export const packagePath = (workspaceId: string, key: string) =>
  `/v1/workspaces/${workspaceId}/solutions/${encodeURIComponent(key)}/package`;
