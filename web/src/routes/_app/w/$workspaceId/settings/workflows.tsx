import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import { builtIns, workflows } from '@/api/queries';
import type { BuiltInState, Resource, Workflow } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { Alert } from '@/components/ui/feedback';
import { SettingsSection, ConfirmDialog } from '@/features/settings/section';
import { QueryFeedback } from '@/features/admin/common';
import { useAccess } from '@/features/lists/access';
import { WorkflowEditor } from '@/features/workflows/workflow-editor';
import { CopyBuiltIn, LaunchWorkflow, WorkflowVersions } from '@/features/workflows/workflow-tools';
import { formDefaults, type FormSchema, type WorkflowDefinition } from '@/features/workflows/model';
import { SchemaFields } from '@/features/workflows/schema-form';
import { automaticSummary, manuallyAvailable, manualSummary, triggerLabel, workspaceTypesQuery } from '@/features/workflows/presentation';
import { useTranslation } from '@/i18n';

const states = ['draft', 'published', 'archived'] as const;

export const Route = createFileRoute('/_app/w/$workspaceId/settings/workflows')({ validateSearch: (search: Record<string, unknown>): { collection?: string } => ({ collection: typeof search.collection === 'string' ? search.collection : undefined }), component: WorkflowsPage });

function WorkflowsPage() {
  const { workspaceId } = Route.useParams();
  const { t } = useTranslation(['workflows', 'common']);
  const search = Route.useSearch();
  const [collectionId, setCollectionId] = useState(search.collection ?? '');
  const client = useQueryClient();
  const list = useQuery({ queryKey: ['workflows', workspaceId], queryFn: () => workflows(workspaceId) });
  const catalog = useQuery({ queryKey: ['workflow-builtins', workspaceId], queryFn: () => builtIns(workspaceId) });
  const collections = useQuery({ queryKey: ['resources', workspaceId, 'workflow-collections'], queryFn: () => allPages<Resource>(`/v1/resources/${workspaceId}/children`) });
  const { canManage } = useAccess(workspaceId);
  const [editing, setEditing] = useState<Workflow | 'new'>();
  const [launching, setLaunching] = useState<Workflow>();
  const [history, setHistory] = useState<Workflow>();
  const [deleting, setDeleting] = useState<Workflow>();
  const refresh = async () => { await client.invalidateQueries({ queryKey: ['workflows', workspaceId] }); await client.invalidateQueries({ queryKey: ['workflow-builtins', workspaceId] }); };
  const remove = useMutation({ mutationFn: (entry: Workflow) => api(`/v1/workflows/${entry.id}`, { method: 'DELETE', ifMatch: entry.version }), onSuccess: async () => { setDeleting(undefined); await refresh(); } });
  return <SettingsSection title={t('page.title')} description={t('page.description')} actions={canManage && <Button variant="primary" onClick={() => setEditing('new')}>{t('editor.newTitle')}</Button>}>
    <QueryFeedback query={list} /><QueryFeedback query={catalog} /><QueryFeedback query={collections} />
    <div className="mb-4 grid gap-2"><Label htmlFor="workflow-collection">{t('page.collection')}</Label><Select id="workflow-collection" value={collectionId} onChange={event => setCollectionId(event.target.value)}><option value="">{t('page.catalogOnly')}</option>{(collections.data ?? []).filter(entry => entry.kind === 'library' || entry.kind === 'list').map(entry => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</Select><p className="text-xs text-muted">{t('page.collectionHint')}</p></div>
    <div className="grid gap-4">
      {(catalog.data ?? []).map(builtin => <BuiltInFeature key={builtin.key} workspaceId={workspaceId} builtin={builtin} collectionId={builtin.per_collection || builtin.role ? collectionId : ''} collections={collections.data ?? []} canManage={canManage} onChanged={refresh} onLaunch={setLaunching} />)}
      {(list.data ?? []).filter(entry => !entry.builtin_key).map(entry => <article key={entry.id} className="grid gap-3 rounded-lg border p-4">
        <div><h3 className="text-sm font-medium">{entry.name}</h3><p className="text-xs text-muted">{t('page.custom', { state: states.includes(entry.state) ? t(`editor.${entry.state}`) : entry.state })}{entry.role && ` · ${t('page.canReplace')}`}</p><p className="mt-1 text-sm">{entry.description}</p></div>
        <UseSummary workflow={entry} collections={collections.data ?? []} />
        <div className="flex flex-wrap gap-2">
          <Button size="sm" disabled={!manuallyAvailable(entry)} onClick={() => setLaunching(entry)}>{t('launch.runOnce')}</Button>
          <Button size="sm" onClick={() => setEditing(entry)}>{canManage ? t('page.configure') : t('page.view')}</Button>
          {canManage && <Button size="sm" onClick={() => setHistory(entry)}>{t('page.versions')}</Button>}
          {canManage && <Button size="sm" variant="danger" onClick={() => setDeleting(entry)}>{t('common:actions.delete')}</Button>}
        </div>
      </article>)}
    </div>
    {editing && <WorkflowEditor workspaceId={workspaceId} workflow={editing === 'new' ? undefined : editing} canManage={canManage} onClose={() => setEditing(undefined)} />}
    {launching && <LaunchWorkflow workflow={launching} onClose={() => setLaunching(undefined)} />}
    {history && <WorkflowVersions workflow={history} canManage={canManage} onEdit={setEditing} onClose={() => setHistory(undefined)} />}
    <ConfirmDialog open={!!deleting} onOpenChange={open => !open && setDeleting(undefined)} title={t('page.deleteTitle', { name: deleting?.name ?? '' })} description={t('page.deleteDescription')} confirm={t('page.deleteConfirm')} busy={remove.isPending} error={remove.error} onConfirm={() => deleting && remove.mutate(deleting)} />
  </SettingsSection>;
}

/** Narrows a built-in's content type pickers to the types of the collection it is set up for. */
function withCollection(schema: FormSchema, collectionId: string): FormSchema {
  if (!collectionId || !schema.properties) return schema;
  return { ...schema, properties: Object.fromEntries(Object.entries(schema.properties).map(([name, property]) => [name, property['x-papergo']?.kind === 'content_type' ? { ...property, 'x-papergo': { collection_id: collectionId, ...property['x-papergo'] } } : property])) };
}

function UseSummary({ workflow, collections }: { workflow: Workflow; collections: Resource[] }) {
  const { t } = useTranslation('workflows');
  const types = useQuery(workspaceTypesQuery(workflow.workspace_id, (workflow.definition as WorkflowDefinition).triggers));
  return <dl className="grid gap-2 text-sm sm:grid-cols-2"><div><dt className="text-xs text-muted">{t('page.manualUse')}</dt><dd>{manualSummary(workflow)}</dd></div><div><dt className="text-xs text-muted">{t('page.automaticRuns')}</dt><dd>{automaticSummary(workflow, collections, types.data)}</dd></div></dl>;
}

function BuiltInFeature({ workspaceId, builtin, collectionId, collections, canManage, onChanged, onLaunch }: { workspaceId: string; builtin: BuiltInState; collectionId: string; collections: Resource[]; canManage: boolean; onChanged: () => Promise<void>; onLaunch: (workflow: Workflow) => void }) {
  const { t } = useTranslation('workflows');
  const scoped = builtin.per_collection || !!builtin.role;
  const existing = builtin.workflows?.find(entry => (entry.collection_id ?? '') === (scoped ? collectionId : ''));
  const [copying, setCopying] = useState(false);
  const schema = (builtin.parameters_schema ?? { type: 'object', properties: {} }) as FormSchema;
  const [parameters, setParameters] = useState<Record<string, unknown>>({});

  const unsupportedScope = builtin.key.startsWith('documents.') && !!collectionId && collections.some(entry => entry.id === collectionId && entry.kind !== 'library');
  const canRun = !unsupportedScope && existing && manuallyAvailable(existing);
  const origin = [t('page.builtIn'), builtin.system && t('page.system'), builtin.default && t('page.productDefault')].filter(Boolean).join(' · ');
  return <article className="grid gap-3 rounded-lg border p-4">
    <div><h3 className="text-sm font-medium">{builtin.name}</h3><p className="text-xs text-muted">{origin}</p><p className="mt-1 text-sm">{builtin.description}</p></div>

    {existing ? <UseSummary workflow={existing} collections={collections} /> : <p className="text-sm">{t('page.notSetUp', { manual: builtin.manual_mode === 'none' ? t('manual.none') : t('page.needsSetup') })}</p>}
    <div className="flex flex-wrap gap-2"><Button size="sm" disabled={!canRun} onClick={() => existing && onLaunch(existing)}>{t('launch.runOnce')}</Button>{canManage && <Button size="sm" disabled={unsupportedScope || !!builtin.per_collection && !collectionId} onClick={() => { setParameters({ ...formDefaults(schema), ...(existing?.parameters ?? {}) }); setCopying(true); }}>{t('copy.title')}</Button>}</div>
    {!existing && builtin.manual_mode !== 'none' && <p className="text-xs text-muted">{canManage ? t('page.prepareManager') : t('page.prepareAsk')}</p>}
    {unsupportedScope ? <p className="text-sm text-muted">{t('page.chooseLibrary')}</p> : <BuiltInSettings key={`${builtin.key}:${collectionId}:${existing?.version ?? 0}`} workspaceId={workspaceId} builtin={builtin} existing={existing} collectionId={collectionId} canManage={canManage} onChanged={onChanged} />}
    {copying && <CopyBuiltIn workspaceId={workspaceId} builtinKey={builtin.key} collectionId={collectionId || undefined} parameters={parameters} onClose={() => setCopying(false)} onCopied={onChanged} />}
  </article>;
}

function BuiltInSettings({ workspaceId, builtin, existing, collectionId, canManage, onChanged }: { workspaceId: string; builtin: BuiltInState; existing?: Workflow; collectionId: string; canManage: boolean; onChanged: () => Promise<void> }) {
  const { t } = useTranslation('workflows');
  const schema = withCollection((builtin.parameters_schema ?? { type: 'object', properties: {} }) as FormSchema, collectionId);
  const [parameters, setParameters] = useState<Record<string, unknown>>({ ...formDefaults(schema), ...(existing?.parameters ?? {}) });
  const [manual, setManual] = useState(existing?.manual_enabled ?? builtin.manual_mode !== 'none');
  const [automatic, setAutomatic] = useState(existing?.automatic_enabled ?? false);
  const triggers = ((existing?.definition as WorkflowDefinition | undefined)?.triggers ?? builtin.automatic_triggers ?? []).filter(trigger => trigger.type !== 'manual');
  const [disabled, setDisabled] = useState(triggers.filter(trigger => trigger.disabled).map(trigger => trigger.type));
  const types = useQuery(workspaceTypesQuery(workspaceId, triggers));
  const automaticScopeReady = !(builtin.per_collection || builtin.role) || !!collectionId;
  const save = useMutation({ mutationFn: () => api(`/v1/workspaces/${workspaceId}/workflow-builtins/${builtin.key}`, { method: 'PUT', ifMatch: existing?.version, body: { collection_id: collectionId || undefined, automatic_enabled: automatic && automaticScopeReady, manual_enabled: manual, state: 'published', disabled_triggers: disabled, parameters } }), onSuccess: async () => { await onChanged(); toast.success(t('settings.saved')); } });
  return <details><summary className="cursor-pointer text-sm font-medium">{t('settings.summary')}</summary><fieldset disabled={!canManage || save.isPending || !!builtin.per_collection && !collectionId} className="mt-3 grid gap-3">
    <label className="flex items-center gap-2 text-sm"><Checkbox checked={manual} disabled={builtin.manual_mode === 'none'} onChange={event => setManual(event.target.checked)} />{t('editor.allowManual')}</label>
    <label className="flex items-center gap-2 text-sm"><Checkbox checked={automatic} disabled={!automaticScopeReady || !triggers.length} onChange={event => setAutomatic(event.target.checked)} />{collectionId ? t('settings.runInCollection') : t('settings.runInService')}</label>
    {!automaticScopeReady && <p className="text-xs text-muted">{t('settings.chooseCollection')}</p>}
    {triggers.length > 0 && <fieldset disabled={!automatic || !automaticScopeReady} className="grid gap-2 border-l pl-3"><legend className="text-xs text-muted">{t('settings.rules')}</legend>{[...new Map(triggers.map(trigger => [trigger.type, trigger])).values()].map(trigger => <label key={trigger.type} className="flex items-center gap-2 text-sm"><Checkbox checked={!disabled.includes(trigger.type)} onChange={event => setDisabled(current => event.target.checked ? current.filter(type => type !== trigger.type) : [...current, trigger.type])} />{triggerLabel(trigger, types.data)}</label>)}</fieldset>}
    <SchemaFields schema={schema} data={parameters} onChange={setParameters} workspaceId={workspaceId} />
    {builtin.role && automatic && <Alert>{t('settings.providerAlert')}</Alert>}
    <QueryFeedback query={save} /><div><Button size="sm" onClick={() => save.mutate()}>{t('settings.save')}</Button></div>
  </fieldset></details>;
}
