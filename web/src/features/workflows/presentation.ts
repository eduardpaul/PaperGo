import { api } from '@/api/http';
import type { Resource, SearchContentType, Workflow } from '@/api/types';
import type { Trigger, WorkflowDefinition } from './model';

/** The catalog types a workspace's lists and libraries use, which name the content types of triggers; fetched only when a trigger has one. */
export const workspaceTypesQuery = (workspaceId: string, triggers: Trigger[] = []) => ({
  queryKey: ['search', 'content-types', workspaceId, undefined],
  enabled: triggers.some(trigger => trigger.content_type_id),
  queryFn: async () => (await api<SearchContentType[]>('/v1/search/content-types', { query: { workspace_id: workspaceId } })).data,
});

/** A trigger's event, narrowed to its content type when it has one. */
export function triggerLabel(trigger: Trigger, types: SearchContentType[] = []): string {
  if (!trigger.content_type_id) return eventLabel(trigger);
  const type = types.find(entry => entry.id === trigger.content_type_id);
  return `${eventLabel(trigger)} (${type ? `${type.name} or a subtype` : 'selected content type'})`;
}

function eventLabel(trigger: Trigger): string {
  const labels: Record<string, string> = { manual: 'Run on demand', call: 'When another workflow calls it', 'file.added': 'When a file is added', 'file.updated': 'When a file is replaced', 'item.created': 'When an item is created', 'item.updated': 'When metadata changes', 'item.published': 'When an item is published', 'permissions.changed': 'When permissions change', 'term.changed': 'When a term changes', 'term.merged': 'When terms merge', 'term.moved': 'When a term moves', 'task.completed': 'When a task is completed' };
  if (trigger.type === 'schedule') {
    if (trigger.cron === '* * * * *') return 'Every minute';
    const daily = /^(\d+) (\d+) \* \* \*$/.exec(trigger.cron ?? '');
    return daily ? `Daily at ${daily[2].padStart(2, '0')}:${daily[1].padStart(2, '0')} (${trigger.time_zone ?? 'UTC'})` : `On a schedule (${trigger.time_zone ?? 'UTC'})`;
  }
  return labels[trigger.type] ?? trigger.type.replaceAll('.', ' · ').replaceAll('_', ' ');
}

export function automaticSummary(workflow: Workflow, collections: Resource[] = [], types: SearchContentType[] = []) {
  if (workflow.state !== 'published') return `Off · ${workflow.state === 'draft' ? 'Draft' : 'Archived'}`;
  // Manual starts and calls do not depend on automatic processing.
  const triggers = (workflow.definition as WorkflowDefinition).triggers.filter(trigger => trigger.type !== 'manual' && trigger.type !== 'call');
  if (!triggers.length) return 'Not configured';
  if (!workflow.automatic_enabled) return 'Off';
  const active = triggers.filter(trigger => !trigger.disabled);
  if (!active.length) return 'Off · All rules paused';
  return `On · ${active.map(trigger => {
    const collection = collections.find(entry => entry.id === trigger.collection_id);
    return `${triggerLabel(trigger, types)}${collection ? ` in ${collection.name}` : trigger.collection_id ? ' in the selected collection' : ''}`;
  }).join('; ')}`;
}

export function manualSummary(workflow: Workflow) {
  if (!(workflow.definition as WorkflowDefinition).triggers.some(trigger => trigger.type === 'manual')) return 'No manual entry point';
  if (workflow.state !== 'published') return workflow.state === 'draft' ? 'Publish to use' : 'Archived';
  return workflow.manual_enabled ? 'Available' : 'Not allowed';
}

export const runStatusLabel = (status: string) => ({ queued: 'Queued', running: 'Running', waiting: 'Waiting', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled' })[status] ?? status;
export const runOriginLabel = (event = 'unknown') => event === 'manual' ? 'Manual' : event === 'schedule' ? 'Scheduled' : event === 'test' ? 'Test' : event === 'call' ? 'Called' : `Automatic · ${triggerLabel({ type: event })}`;
