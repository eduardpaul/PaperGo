import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { api } from '@/api/http';
import { parseJSON, stringifyJSON } from '@/api/json';
import { collectionChildren, keys, resourceQuery, templatesQuery, termsById, workspacesQuery } from '@/api/queries';
import type { FieldDefinition, FilterExpr, Page, SmartFolder, SmartFolderDefinition, SmartFolderEntry } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Alert } from '@/components/ui/feedback';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { KeywordInput } from '@/components/keyword-input';
import { can } from '@/features/lists/access';
import { fileTypeField, FilterEditor, normalizeFilter, systemFields } from '@/features/lists/filter-editor';
import { problemMessage } from '@/lib/errors';
import { useDebounced } from '@/lib/use-debounced';

export interface SmartQueryField {
  key: string; label: string; type: string; scale?: number; queryable: boolean; multiple?: boolean; choices?: string[]; term_set_id?: string; lookup_container_id?: string;
}
export function smartQueryFields(entries: SmartQueryField[]): FieldDefinition[] {
  const byKey = new Map<string, SmartQueryField[]>();
  for (const field of entries.filter(field => field.queryable)) byKey.set(field.key, [...(byKey.get(field.key) ?? []), field]);
  return [...systemFields, fileTypeField(), ...[...byKey.values()].filter(fields => new Set(fields.map(field => [field.type, field.scale, !!field.multiple, field.term_set_id, field.lookup_container_id].join(':'))).size === 1).map(fields => ({
    id: fields[0].key, key: fields[0].key, label: fields[0].label, type: fields[0].type, scale: fields[0].scale, indexed: true,
    choices: [...new Set(fields.flatMap(field => field.choices ?? []))], options: { multiple: fields[0].multiple, term_set_id: fields[0].term_set_id, lookup_container_id: fields[0].lookup_container_id },
  }))];
}

export function SmartFolderDialog({ open, folder, onClose, onSaved }: { open: boolean; folder?: SmartFolder; onClose: () => void; onSaved: (folder: SmartFolder) => void }) {
  return open ? <Editor key={folder ? `${folder.id}:${folder.version}` : 'new'} folder={folder} onClose={onClose} onSaved={onSaved} /> : null;
}

function Editor({ folder, onClose, onSaved }: { folder?: SmartFolder; onClose: () => void; onSaved: (folder: SmartFolder) => void }) {
  const initial = folder?.definition;
  const client = useQueryClient();
  const workspaces = useQuery(workspacesQuery);
  const canWrite = useHasScope('item.write');
  const taxonomy = useHasScope('taxonomy.read');
  const [name, setName] = useState(folder?.name ?? '');
  const [description, setDescription] = useState(folder?.description ?? '');
  const [personal, setPersonal] = useState(folder ? !!folder.owner_id : true);
  const [workspaceId, setWorkspaceId] = useState(folder?.workspace_id ?? '');
  const [mode, setMode] = useState<SmartFolderDefinition['mode']>(initial?.mode ?? 'query');
  const [kind, setKind] = useState(initial?.collection_kind ?? '');
  const [collections, setCollections] = useState(initial?.collections ?? []);
  const [templates, setTemplates] = useState(initial?.templates ?? []);
  const [contentTypes, setContentTypes] = useState(initial?.content_types ?? []);
  const [terms, setTerms] = useState(initial?.terms ?? []);
  const [termMatch, setTermMatch] = useState(initial?.term_match ?? 'all');
  const [archive, setArchive] = useState<SmartFolderDefinition['archive']>(initial?.archive ?? 'active');
  const [includeFolders, setIncludeFolders] = useState(!!initial?.include_folders);
  const [filter, setFilter] = useState<FilterExpr | undefined>(initial?.filter);
  const [groups, setGroups] = useState(initial?.group_by ?? []);
  const workspace = useQuery({ ...resourceQuery(workspaceId), enabled: !!workspaceId });
  const places = useQuery({ queryKey: ['resources', workspaceId, 'collections'], queryFn: () => collectionChildren(workspaceId), enabled: !!workspaceId });
  const selectedPlace = collections.length === 1 ? places.data?.find(place => place.name === collections[0])?.id : undefined;
  const metadata = useQuery({ queryKey: ['resources', 'smart-folder-fields', workspaceId || undefined, selectedPlace], queryFn: async () => (await api<Page<SmartQueryField>>('/v1/search/fields', { query: { workspace_id: workspaceId, collection_id: selectedPlace } })).data.value });
  const fields = useMemo(() => smartQueryFields(metadata.data ?? []), [metadata.data]);
  const types = useQuery({ queryKey: ['search', 'content-types', workspaceId || undefined, selectedPlace], queryFn: async () => (await api<{ id: string; key: string; name: string }[]>('/v1/search/content-types', { query: { workspace_id: workspaceId, collection_id: selectedPlace } })).data });
  const templateList = useQuery(templatesQuery);
  const typeOptions = (types.data ?? []).map(type => ({ value: type.key, label: type.name }));
  let definition: SmartFolderDefinition;
  let invalid: string | undefined;
  try {
    definition = { archive, mode, collection_kind: kind ? kind as 'list' | 'library' : undefined, collections, templates, content_types: contentTypes, terms, term_match: termMatch, include_folders: includeFolders, filter: normalizeFilter(filter, fields), group_by: groups };
  } catch (error) {
    invalid = problemMessage(error);
    definition = { mode };
  }
  const body = { name: name.trim(), description: description.trim(), personal, workspace_id: workspaceId || undefined, definition };
  const previewInput = stringifyJSON({ workspace_id: body.workspace_id, definition })!;
  const delayed = useDebounced(previewInput, 350);
  const updating = delayed !== previewInput;
  const preview = useQuery({ queryKey: ['smart-folder-preview', delayed], enabled: !invalid && (personal || !!workspaceId), queryFn: async () => (await api<Page<SmartFolderEntry>>('/v1/smart-folders/preview', { method: 'POST', body: parseJSON(delayed) })).data, staleTime: 30_000 });
  const save = useMutation({
    meta: { silent: true },
    mutationFn: () => api<SmartFolder>(folder ? `/v1/smart-folders/${folder.id}` : '/v1/smart-folders', { method: folder ? 'PUT' : 'POST', ifMatch: folder?.version, body }).then(result => result.data),
    onSuccess: async result => { await client.invalidateQueries({ queryKey: keys.smartFolders }); onSaved(result); },
  });
  const keywordAssignment = useMutation({
    mutationFn: async (values: string[]) => {
      const known = new Set((await termsById(values)).map(term => term.id));
      return [...new Set(await Promise.all(values.map(async value => known.has(value) ? value : (await api<{ id: string }>('/v1/keywords', { method: 'POST', body: { name: value } })).data.id)))];
    },
    onSuccess: async values => { setTerms(values); await client.invalidateQueries({ queryKey: ['keywords'] }); },
  });
  const allowed = canWrite && (personal || can(workspace.data, 'manage'));
  const ready = !keywordAssignment.isPending && allowed && !!name.trim() && !invalid && !updating && preview.isSuccess;
  return <FormDialog open title={folder ? 'Edit smart folder rules' : 'New smart folder'} description="Choose rules using your collections and fields, then preview the matching items." submitLabel={folder ? 'Save rules' : 'Create'} pending={save.isPending} submitDisabled={!ready} onClose={onClose} onSubmit={() => ready && save.mutate()}>
    {save.error && <Alert>{problemMessage(save.error)}</Alert>}
    <Label htmlFor="smart-name">Name</Label><Input id="smart-name" value={name} onChange={event => setName(event.target.value)} required autoFocus />
    <Label htmlFor="smart-description">Description (optional)</Label><Textarea id="smart-description" value={description} onChange={event => setDescription(event.target.value)} />
    <fieldset className="grid gap-2 rounded-md border p-3"><legend className="px-1 text-sm font-medium">Purpose</legend>
      <label className="flex gap-2"><input type="radio" name="smart-purpose" checked={mode === 'query'} onChange={() => setMode('query')} />Saved search</label><p className="text-xs text-muted">Items matching these rules appear automatically.</p>
      <label className="flex gap-2"><input type="radio" name="smart-purpose" checked={mode === 'classification'} onChange={() => setMode('classification')} />Classification folder</label><p className="text-xs text-muted">Matching items appear automatically. You can also classify an item by applying the folder’s keywords and equality values.</p>
    </fieldset>
    <Label htmlFor="smart-sharing">Ownership</Label><Select id="smart-sharing" value={personal ? 'personal' : 'shared'} disabled={!!folder} onChange={event => { setPersonal(event.target.value === 'personal'); setWorkspaceId(''); setCollections([]); }}><option value="personal">Personal — only me</option><option value="shared">Shared with a workspace</option></Select>
    <Label htmlFor="smart-workspace">Search in</Label><Select id="smart-workspace" value={workspaceId} disabled={!!folder} onChange={event => { setWorkspaceId(event.target.value); setCollections([]); }}>
      <option value="">{personal ? 'All readable workspaces' : 'Choose a workspace…'}</option>
      {workspaces.data?.filter(workspace => personal || !workspace.personal_owner_id).map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.personal_owner_id ? 'Home' : workspace.name}</option>)}
    </Select>
    {!personal && workspaceId && !can(workspace.data, 'manage') && <p className="text-xs text-muted">Workspace managers can save shared smart folders.</p>}
    <Label htmlFor="smart-archive">Items</Label><Select id="smart-archive" value={archive} onChange={event => setArchive(event.target.value as SmartFolderDefinition['archive'])}><option value="active">Active</option><option value="archived">Archived</option><option value="all">All</option></Select>
    <Label htmlFor="smart-kind">Collection type</Label><Select id="smart-kind" value={kind} onChange={event => setKind(event.target.value)}><option value="">Lists and libraries</option><option value="list">Lists</option><option value="library">Libraries</option></Select>
    {workspaceId && <><Label id="smart-collections-label">Collections (optional)</Label><Combobox aria-labelledby="smart-collections-label" multiple selected={collections.map(value => ({ value, label: value }))} options={[...new Set((places.data ?? []).filter(place => !kind || place.kind === kind).map(place => place.name))].map(value => ({ value, label: value }))} onChange={options => setCollections(options.map(option => option.value))} /></>}
    <Label id="smart-types-label">Content types (optional)</Label><Combobox aria-labelledby="smart-types-label" multiple selected={contentTypes.map(value => typeOptions.find(option => option.value.toLowerCase() === value.toLowerCase()) ?? { value, label: value })} options={typeOptions} onChange={options => setContentTypes(options.map(option => option.value))} />
    <label className="flex gap-2 text-sm"><input type="checkbox" checked={includeFolders} onChange={event => setIncludeFolders(event.target.checked)} />Include physical folders</label>
    <section className="grid gap-2"><h3 className="font-medium">Rules</h3><QueryFeedback query={metadata} /><FilterEditor fields={fields} filter={filter} onChange={setFilter} /><p className="text-xs text-muted">Custom rules use filterable fields. Choose a collection to resolve fields with different definitions.</p></section>
    {taxonomy && <section className="grid gap-2"><Label htmlFor="smart-keywords">Keywords (optional)</Label><KeywordInput id="smart-keywords" value={terms} disabled={keywordAssignment.isPending} onChange={values => keywordAssignment.mutate(values)} />{keywordAssignment.error && <Alert>{problemMessage(keywordAssignment.error)}</Alert>}<Select aria-label="Match keywords" value={termMatch} onChange={event => setTermMatch(event.target.value)}><option value="all">All selected keywords</option><option value="any">Any selected keyword</option></Select></section>}
    <details><summary className="cursor-pointer text-sm">Optional grouping and templates</summary><div className="grid gap-2 pt-2">
      <Label id="smart-templates-label">Collection templates</Label><Combobox aria-labelledby="smart-templates-label" multiple selected={templates.map(value => ({ value, label: templateList.data?.find(template => template.key === value)?.name ?? value }))} options={(templateList.data ?? []).map(template => ({ value: template.key, label: template.name }))} onChange={options => setTemplates(options.map(option => option.value))} />
      {groups.map((group, index) => <div key={index} className="flex gap-2"><Select aria-label={`Group level ${index + 1}`} value={group.field} onChange={event => setGroups(groups.map((group, i) => i === index ? { field: event.target.value } : group))}>{fields.filter(field => field.key !== '$file_type' && !field.options?.multiple).map(field => <option key={field.key} value={field.key}>{field.label}</option>)}</Select>{['date', 'datetime'].includes(fields.find(field => field.key === group.field)?.type ?? '') && <Select aria-label={`Group period ${index + 1}`} value={group.by ?? ''} onChange={event => setGroups(groups.map((group, i) => i === index ? { ...group, by: event.target.value || undefined } : group))}><option value="">Exact date</option><option value="year">Year</option><option value="month">Month</option></Select>}<Button onClick={() => setGroups(groups.filter((_, i) => i !== index))}>Remove level</Button></div>)}
      <Button disabled={groups.length >= 3} onClick={() => setGroups([...groups, { field: '$modified_at', by: 'year' }])}>Add grouping level</Button><p className="text-xs text-muted">Results open directly. Group browsing is available when requested.</p>
    </div></details>
    <section aria-label="Match preview" className="grid gap-2 rounded-md border p-3"><h3 className="font-medium">Match preview</h3>
      {invalid ? <Alert>{invalid}</Alert> : updating ? <p role="status">Updating preview…</p> : personal || workspaceId ? <><QueryFeedback query={preview} />{preview.data && !preview.isError && <><p role="status">{preview.data.total ?? 0}{preview.data.total_capped ? '+' : ''} matching items</p><ul>{preview.data.value?.map(entry => <li key={entry.item.id} className="flex justify-between gap-2 text-sm"><span>{entry.item.name}</span><span className="text-xs text-muted">{entry.collection_name}</span></li>)}</ul></>}</> : <p className="text-sm text-muted">Choose a workspace to preview shared rules.</p>}
    </section>
  </FormDialog>;
}
