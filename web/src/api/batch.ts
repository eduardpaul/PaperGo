import { api, ApiError } from './http';
export interface BatchCall {
  id: string;
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: unknown;
}
interface BatchResult {
  id: string;
  status: number;
  body?: { detail?: string; code?: string };
}

/** Process every response: a successful batch envelope can contain failed requests. */
export async function batch(calls: BatchCall[]): Promise<void> {
  const failures: BatchResult[] = [];
  for (let index = 0; index < calls.length; index += 20) {
    const chunk = calls.slice(index, index + 20);
    const result = (
      await api<{ responses: BatchResult[] }>('/v1/$batch', { method: 'POST', body: { requests: chunk } })
    ).data;
    for (const call of chunk) {
      const response = result.responses.find((response) => response.id === call.id);
      if (!response) failures.push({ id: call.id, status: 500, body: { detail: 'No result returned.' } });
      else if (response.status >= 400) failures.push(response);
    }
  }
  if (failures.length)
    throw new ApiError(
      failures[0].status,
      failures[0].body?.code ?? 'batch_failed',
      `${failures.length} actions failed: ${failures.map((failure) => failure.body?.detail ?? `HTTP ${failure.status}`).join('; ')}`,
    );
}
