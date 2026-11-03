import { api } from '@/api/http';
import type { Resource, SearchContentType, Workflow } from '@/api/types';
import { i18n } from '@/i18n';
import type { Trigger, WorkflowDefinition } from './model';

// These run while screens render, so the text follows the language of the screen that shows it.

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
  return type
    ? i18n.t('workflows:trigger.withType', { event: eventLabel(trigger), type: type.name })
    : i18n.t('workflows:trigger.withSelectedType', { event: eventLabel(trigger) });
}

const events = ['manual', 'call', 'file.added', 'file.updated', 'item.created', 'item.updated', 'item.published', 'permissions.changed', 'term.changed', 'term.merged', 'term.moved', 'task.completed'] as const;

function eventLabel(trigger: Trigger): string {
  if (trigger.type === 'schedule') {
    const timeZone = trigger.time_zone ?? 'UTC';
    if (trigger.cron === '* * * * *') return i18n.t('workflows:trigger.everyMinute');
    const daily = /^(\d+) (\d+) \* \* \*$/.exec(trigger.cron ?? '');
    return daily
      ? i18n.t('workflows:trigger.daily', { time: `${daily[2].padStart(2, '0')}:${daily[1].padStart(2, '0')}`, timeZone })
      : i18n.t('workflows:trigger.schedule', { timeZone });
  }
  const known = events.find(event => event === trigger.type);
  return known ? i18n.t(`workflows:trigger.event.${known}`) : trigger.type.replaceAll('.', ' · ').replaceAll('_', ' ');
}

export function automaticSummary(workflow: Workflow, collections: Resource[] = [], types: SearchContentType[] = []) {
  if (workflow.state !== 'published') return workflow.state === 'draft' ? i18n.t('workflows:automatic.offDraft') : i18n.t('workflows:automatic.offArchived');
  // Manual starts and calls do not depend on automatic processing.
  const triggers = (workflow.definition as WorkflowDefinition).triggers.filter(trigger => trigger.type !== 'manual' && trigger.type !== 'call');
  if (!triggers.length) return i18n.t('workflows:automatic.notConfigured');
  if (!workflow.automatic_enabled) return i18n.t('workflows:automatic.off');
  const active = triggers.filter(trigger => !trigger.disabled);
  if (!active.length) return i18n.t('workflows:automatic.paused');
  return i18n.t('workflows:automatic.on', { rules: active.map(trigger => {
    const collection = collections.find(entry => entry.id === trigger.collection_id);
    const label = triggerLabel(trigger, types);
    return collection
      ? i18n.t('workflows:automatic.inCollection', { rule: label, collection: collection.name })
      : trigger.collection_id ? i18n.t('workflows:automatic.inSelectedCollection', { rule: label }) : label;
  }).join('; ') });
}

/** Whether people can start the workflow by hand now. */
export function manuallyAvailable(workflow: Workflow) {
  return workflow.state === 'published' && workflow.manual_enabled && (workflow.definition as WorkflowDefinition).triggers.some(trigger => trigger.type === 'manual');
}

export function manualSummary(workflow: Workflow) {
  if (!(workflow.definition as WorkflowDefinition).triggers.some(trigger => trigger.type === 'manual')) return i18n.t('workflows:manual.none');
  if (workflow.state !== 'published') return workflow.state === 'draft' ? i18n.t('workflows:manual.publishToUse') : i18n.t('workflows:manual.archived');
  return workflow.manual_enabled ? i18n.t('workflows:manual.available') : i18n.t('workflows:manual.notAllowed');
}

const statuses = ['queued', 'running', 'waiting', 'completed', 'failed', 'cancelled'] as const;

export const runStatusLabel = (status: string) => {
  const known = statuses.find(entry => entry === status);
  return known ? i18n.t(`workflows:run.status.${known}`) : status;
};

export const runOriginLabel = (event = 'unknown') =>
  event === 'manual' ? i18n.t('workflows:run.origin.manual')
    : event === 'schedule' ? i18n.t('workflows:run.origin.schedule')
      : event === 'test' ? i18n.t('workflows:run.origin.test')
        : event === 'call' ? i18n.t('workflows:run.origin.call')
          : i18n.t('workflows:run.origin.automatic', { event: triggerLabel({ type: event }) });
