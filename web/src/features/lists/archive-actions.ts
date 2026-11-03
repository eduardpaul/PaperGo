import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import type { Resource } from '@/api/types';

export function useArchiveActions(onDone?: (completed: string[]) => void) {
  const { t } = useTranslation('lists');
  const client = useQueryClient();
  return useMutation({
    meta: { silent: true },
    mutationFn: async ({ items, archived }: { items: Resource[]; archived: boolean }) => {
      const groups = new Map<string, Resource[]>();
      for (const item of items) {
        if (item.kind !== 'item' || !item.container_id) throw new Error(t('archive.onlyItems'));
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
      if (failures.length) throw new Error([...(completed.length ? [t('archive.updated', { count: completed.length })] : []), ...failures].join(' '));
      return completed;
    },
    onSuccess: (completed, { archived }) => { toast.success(archived ? t('archive.archived', { count: completed.length }) : t('archive.unarchived', { count: completed.length })); },
    onError: error => toast.error(problemMessage(error)),
    onSettled: () => client.invalidateQueries(),
  });
}
