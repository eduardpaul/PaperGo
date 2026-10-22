import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { problemMessage } from '@/lib/errors';
import type { Resource } from '@/api/types';

export function useArchiveActions(onDone?: (completed: string[]) => void) {
  const client = useQueryClient();
  return useMutation({
    meta: { silent: true },
    mutationFn: async ({ items, archived }: { items: Resource[]; archived: boolean }) => {
      const groups = new Map<string, Resource[]>();
      for (const item of items) {
        if (item.kind !== 'item' || !item.container_id) throw new Error('Only documents and list items can be archived.');
        const group = groups.get(item.container_id) ?? [];
        group.push(item); groups.set(item.container_id, group);
      }
      const completed: string[] = []; const failures: string[] = [];
      for (const [collection, group] of groups) {
        for (let start = 0; start < group.length; start += 100) {
          const chunk = group.slice(start, start + 100);
          try {
            await api(`/v1/resources/${collection}/bulk`, { method: 'POST', body: { operations: chunk.map(item => ({ action: archived ? 'archive' : 'unarchive', id: item.id, version: item.version })) } });
            completed.push(...chunk.map(item => item.id));
          } catch (error) { failures.push(problemMessage(error)); }
        }
      }
      onDone?.(completed);
      if (failures.length) throw new Error(`${completed.length ? `${completed.length} updated. ` : ''}${failures.join(' ')}`);
      return completed;
    },
    onSuccess: (completed, { archived }) => { toast.success(`${completed.length} ${completed.length === 1 ? 'item' : 'items'} ${archived ? 'archived' : 'unarchived'}`); },
    onError: error => toast.error(problemMessage(error)),
    onSettled: () => client.invalidateQueries(),
  });
}
