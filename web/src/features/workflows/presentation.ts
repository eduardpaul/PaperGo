import type { Resource, Workflow } from '@/api/types';
import type { Trigger, WorkflowDefinition } from './model';

export function triggerLabel(trigger: Trigger): string {
  const labels: Record<string, string> = { manual: 'Run on demand', 'file.added': 'When a file is added', 'file.updated': 'When a file is replaced', 'item.created': 'When an item is created', 'item.updated': 'When metadata changes', 'item.published': 'When an item is published', 'permissions.changed': 'When permissions change', 'term.changed': 'When a term changes', 'term.merged': 'When terms merge', 'task.completed': 'When a task is completed' };
  if (trigger.type === 'schedule') {
    if (trigger.cron === '* * * * *') return 'Every minute';
    const daily = /^(\d+) (\d+) \* \* \*$/.exec(trigger.cron ?? '');
    return daily ? `Daily at ${daily[2].padStart(2, '0')}:${daily[1].padStart(2, '0')} (${trigger.time_zone ?? 'UTC'})` : `On a schedule (${trigger.time_zone ?? 'UTC'})`;
  }
  return labels[trigger.type] ?? trigger.type.replaceAll('.', ' · ').replaceAll('_', ' ');
}

export function automaticSummary(workflow: Workflow, collections: Resource[] = []) {
  if (workflow.state !== 'published') return `Off · ${workflow.state === 'draft' ? 'Draft' : 'Archived'}`;
  const triggers = (workflow.definition as WorkflowDefinition).triggers.filter(trigger => trigger.type !== 'manual');
  if (!triggers.length) return 'Not configured';
  if (!workflow.automatic_enabled) return 'Off';
  const active = triggers.filter(trigger => !trigger.disabled);
  if (!active.length) return 'Off · All rules paused';
  return `On · ${active.map(trigger => {
    const collection = collections.find(entry => entry.id === trigger.collection_id);
    return `${triggerLabel(trigger)}${collection ? ` in ${collection.name}` : trigger.collection_id ? ' in the selected collection' : ''}`;
  }).join('; ')}`;
}

export function manualSummary(workflow: Workflow) {
  if (!(workflow.definition as WorkflowDefinition).triggers.some(trigger => trigger.type === 'manual')) return 'No manual entry point';
  if (workflow.state !== 'published') return workflow.state === 'draft' ? 'Publish to use' : 'Archived';
  return workflow.manual_enabled ? 'Available' : 'Not allowed';
}

export const runStatusLabel = (status: string) => ({ queued: 'Queued', running: 'Running', waiting: 'Waiting', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled' })[status] ?? status;
export const runOriginLabel = (event = 'unknown') => event === 'manual' ? 'Manual' : event === 'schedule' ? 'Scheduled' : `Automatic · ${triggerLabel({ type: event })}`;
