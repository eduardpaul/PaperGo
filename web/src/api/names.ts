import { api, isApiError } from './http';
import { termsById } from './queries';

/** Labels of term and keyword ids. Ids the store does not return read "Unknown term"; without taxonomy access, none. */
export async function termNames(ids: string[]): Promise<Map<string, string>> {
  const names = new Map<string, string>();
  try {
    for (let index = 0; index < ids.length; index += 200) {
      const chunk = ids.slice(index, index + 200);
      const found = await termsById(chunk);
      for (const term of found) names.set(term.id, term.name);
      for (const id of chunk) if (!names.has(id)) names.set(id, 'Unknown term');
    }
  } catch (error) {
    if (!isApiError(error, 403)) throw error;
  }
  return names;
}

interface BatchResponse {
  id: string;
  status: number;
  body?: { name?: string };
}

/** Names of looked-up items, read 20 at a time through $batch. Items the caller cannot read are "Unavailable item". */
export async function resourceNames(ids: string[]): Promise<Map<string, string>> {
  const names = new Map<string, string>();
  const chunks: string[][] = [];
  for (let index = 0; index < ids.length; index += 20) chunks.push(ids.slice(index, index + 20));
  await Promise.all(
    chunks.map(async (chunk) => {
      const result = (
        await api<{ responses: BatchResponse[] }>('/v1/$batch', {
          method: 'POST',
          body: { requests: chunk.map((id) => ({ id, method: 'GET', url: `/v1/resources/${id}` })) },
        })
      ).data;
      for (const id of chunk) {
        const response = result.responses.find((entry) => entry.id === id);
        names.set(id, response?.status === 200 && response.body?.name ? response.body.name : 'Unavailable item');
      }
    }),
  );
  return names;
}

/**
 * Reads names one id at a time for the caller, but together for every id asked for in the same tick: each id is its
 * own cache entry, and the ids a screen needs at once still go in a few requests. Unknown ids resolve to null.
 */
export function nameLoader(readMany: (ids: string[]) => Promise<Map<string, string>>) {
  type Waiter = { resolve: (name: string | null) => void; reject: (error: unknown) => void };
  let pending = new Map<string, Waiter[]>();
  async function flush() {
    const batch = pending;
    pending = new Map();
    try {
      const names = await readMany([...batch.keys()]);
      for (const [id, waiters] of batch) for (const waiter of waiters) waiter.resolve(names.get(id) ?? null);
    } catch (error) {
      for (const waiters of batch.values()) for (const waiter of waiters) waiter.reject(error);
    }
  }
  return (id: string) =>
    new Promise<string | null>((resolve, reject) => {
      if (pending.size === 0) setTimeout(() => void flush(), 0);
      pending.set(id, [...(pending.get(id) ?? []), { resolve, reject }]);
    });
}

export const loadResourceName = nameLoader(resourceNames);
export const loadTermName = nameLoader(termNames);
