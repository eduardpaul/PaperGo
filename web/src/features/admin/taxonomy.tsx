import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Pencil, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Textarea } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { Field, QueryFeedback, useHasScope } from './common';
import { PackageTransfer } from './package-transfer';

export interface TermGroup {
  id: string;
  name: string;
  description: string;
  is_system?: boolean;
  version: number;
}
export interface TermSet {
  id: string;
  group_id: string;
  key: string;
  name: string;
  description: string;
  is_open: boolean;
  is_keywords?: boolean;
  version: number;
}
export interface ManagedTerm {
  id: string;
  term_set_id: string;
  parent_id?: string;
  name: string;
  description?: string;
  color?: string;
  sort_order?: number;
  synonyms?: string[];
  labels?: Record<string, string>;
  deprecated?: boolean;
  available_as_keyword?: boolean;
  has_children: boolean;
  version: number;
}
export const termSetsQuery = { queryKey: ['termStore', 'sets'], queryFn: () => allPages<TermSet>('/v1/term-sets') };
const groupQuery = { queryKey: ['termStore', 'groups'], queryFn: () => allPages<TermGroup>('/v1/term-groups') };

export function TermStore() {
  const client = useQueryClient();
  const canRead = useHasScope('taxonomy.read');
  const canEdit = useHasScope('taxonomy.manage');
  const groups = useQuery({ ...groupQuery, enabled: canRead });
  const sets = useQuery({ ...termSetsQuery, enabled: canRead });
  const [setId, setSetId] = useState('');
  const [editing, setEditing] = useState<{ kind: 'group' | 'set'; groupId?: string; entity?: TermGroup | TermSet }>();
  const [deleting, setDeleting] = useState<{ kind: 'group' | 'set'; entity: TermGroup | TermSet }>();
  const [importGroup, setImportGroup] = useState('');
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: ['termStore'] });
  };
  const remove = useMutation({
    mutationFn: () =>
      api(`/v1/term-${deleting!.kind}s/${deleting!.entity.id}`, {
        method: 'DELETE',
        ifMatch: deleting!.entity.version,
      }),
    onSuccess: async () => {
      setDeleting(undefined);
      await refresh();
    },
  });
  const selected = sets.data?.find((set) => set.id === setId);
  if (!canRead) return <Alert>You need taxonomy.read to view the term store.</Alert>;
  return (
    <div className="flex flex-col gap-5">
      <div className="grid gap-5 lg:grid-cols-[18rem_1fr]">
        <SettingsSection
          title="Term sets"
          actions={
            canEdit && (
              <Button onClick={() => setEditing({ kind: 'group' })}>
                <Plus /> Group
              </Button>
            )
          }
        >
          <QueryFeedback query={groups} />
          <QueryFeedback query={sets} />
          <ul className="grid gap-3">
            {groups.data?.map((group) => (
              <li key={group.id}>
                <div className="flex flex-wrap items-center gap-1">
                  <span className="flex-1 font-medium">{group.name}</span>
                  {canEdit && !group.is_system && (
                    <>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={`Edit ${group.name}`}
                        onClick={() => setEditing({ kind: 'group', entity: group })}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={`New set in ${group.name}`}
                        onClick={() => setEditing({ kind: 'set', groupId: group.id })}
                      >
                        <Plus />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={`Delete ${group.name}`}
                        onClick={() => setDeleting({ kind: 'group', entity: group })}
                      >
                        <Trash2 />
                      </Button>
                      <Button size="sm" onClick={() => setImportGroup(group.id)}>
                        Import CSV
                      </Button>
                    </>
                  )}
                </div>
                <ul>
                  {sets.data
                    ?.filter((set) => set.group_id === group.id)
                    .map((set) => (
                      <li key={set.id}>
                        <button
                          type="button"
                          className={`w-full rounded px-2 py-2 text-left ${set.id === setId ? 'bg-accent-soft text-accent' : 'hover:bg-surface-muted'}`}
                          onClick={() => setSetId(set.id)}
                        >
                          {set.name}
                          {set.is_keywords && <Badge className="ml-2">Keywords</Badge>}
                        </button>
                      </li>
                    ))}
                </ul>
              </li>
            ))}
          </ul>
        </SettingsSection>
        {selected ? (
          <SettingsSection
            title={selected.name}
            description={selected.is_open ? 'People can add terms when assigning metadata.' : 'Managed terms.'}
            actions={
              canEdit &&
              !selected.is_keywords && (
                <>
                  <Button onClick={() => setEditing({ kind: 'set', entity: selected, groupId: selected.group_id })}>
                    Edit set
                  </Button>
                  <Button variant="danger" onClick={() => setDeleting({ kind: 'set', entity: selected })}>
                    Delete set
                  </Button>
                </>
              )
            }
          >
            <TermLevel key={selected.id} set={selected} canEdit={canEdit} />
            {selected.is_keywords && (
              <KeywordPromotion sets={sets.data?.filter((set) => !set.is_keywords) ?? []} canEdit={canEdit} />
            )}
          </SettingsSection>
        ) : (
          <SettingsSection
            title="Choose a term set"
            description="Shared keywords and categories for managed metadata and free keywords."
          />
        )}
      </div>
      <PackageTransfer title="Taxonomy" path="/v1/taxonomy" canImport={canEdit} onImported={refresh} />
      {editing && <TaxonomyNameDialog {...editing} onClose={() => setEditing(undefined)} onSaved={refresh} />}
      {importGroup && <CSVImport groupId={importGroup} onClose={() => setImportGroup('')} onSaved={refresh} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={`Delete ${deleting?.entity.name ?? ''}?`}
        description="The server checks that no fields or terms still reference this definition."
        confirm="Delete"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </div>
  );
}

function TaxonomyNameDialog({
  kind,
  groupId,
  entity,
  onClose,
  onSaved,
}: {
  kind: 'group' | 'set';
  groupId?: string;
  entity?: TermGroup | TermSet;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(entity?.name ?? '');
  const [key, setKey] = useState(entity && 'key' in entity ? entity.key : '');
  const [description, setDescription] = useState(entity?.description ?? '');
  const [open, setOpen] = useState(entity && 'is_open' in entity ? entity.is_open : false);
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/term-${kind}s${entity ? `/${entity.id}` : ''}`, {
        method: entity ? 'PUT' : 'POST',
        ifMatch: entity?.version,
        body: {
          name: name.trim(),
          description,
          group_id: kind === 'set' ? groupId : undefined,
          key: kind === 'set' ? key.trim() : undefined,
          is_open: kind === 'set' ? open : undefined,
        },
      }),
    onSuccess: async () => {
      await onSaved();
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={`${entity ? 'Edit' : 'New'} term ${kind}`}
      submitLabel="Save"
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Field id="taxonomy-name" label="Name">
        <Input id="taxonomy-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      {kind === 'set' && (
        <Field id="taxonomy-key" label="Key">
          <Input
            id="taxonomy-key"
            required
            disabled={!!entity}
            value={key}
            onChange={(event) => setKey(event.target.value)}
          />
        </Field>
      )}
      <Field id="taxonomy-description" label="Description">
        <Textarea
          id="taxonomy-description"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </Field>
      {kind === 'set' && (
        <label className="flex gap-2">
          <Checkbox checked={open} onChange={(event) => setOpen(event.target.checked)} />
          Allow people to add terms when assigning metadata
        </label>
      )}
    </FormDialog>
  );
}

function TermLevel({ set, parentId, canEdit }: { set: TermSet; parentId?: string; canEdit: boolean }) {
  const [editing, setEditing] = useState<ManagedTerm | 'new'>();
  const [expanded, setExpanded] = useState('');
  const terms = useQuery({
    queryKey: ['termStore', 'terms', set.id, parentId ?? 'root'],
    queryFn: () =>
      allPages<ManagedTerm>(`/v1/term-sets/${set.id}/terms`, { parent_id: parentId, include_deprecated: 'true' }),
  });
  return (
    <div className="border-l pl-3">
      <QueryFeedback query={terms} />
      <ul>
        {terms.data?.map((term) => (
          <li key={term.id}>
            <div className="flex items-center gap-2 py-2">
              <Button
                size="icon"
                variant="ghost"
                aria-label={`Expand ${term.name}`}
                disabled={!term.has_children}
                onClick={() => setExpanded(expanded === term.id ? '' : term.id)}
              >
                <ChevronRight />
              </Button>
              <span className="flex-1" style={{ color: term.color || undefined }}>
                {term.name} {term.deprecated && <Badge>Deprecated</Badge>}
              </span>
              {canEdit && (
                <Button size="icon" variant="ghost" aria-label={`Edit ${term.name}`} onClick={() => setEditing(term)}>
                  <Pencil />
                </Button>
              )}
            </div>
            {expanded === term.id && <TermLevel set={set} parentId={term.id} canEdit={canEdit} />}
          </li>
        ))}
      </ul>
      {canEdit && (
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus /> New term {parentId ? 'below' : ''}
        </Button>
      )}
      {editing && (
        <TermDialog
          set={set}
          term={editing === 'new' ? undefined : editing}
          parentId={parentId}
          onClose={() => setEditing(undefined)}
        />
      )}
    </div>
  );
}

function TermDialog({
  set,
  term,
  parentId,
  onClose,
}: {
  set: TermSet;
  term?: ManagedTerm;
  parentId?: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState(term?.name ?? '');
  const [description, setDescription] = useState(term?.description ?? '');
  const [color, setColor] = useState(term?.color ?? '');
  const [synonyms, setSynonyms] = useState((term?.synonyms ?? []).join(', '));
  const [deprecated, setDeprecated] = useState(!!term?.deprecated);
  const [keyword, setKeyword] = useState(!!term?.available_as_keyword);
  const [target, setTarget] = useState('');
  const [parent, setParent] = useState(term?.parent_id ?? '');
  const done = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ['termStore'] }),
      client.invalidateQueries({ queryKey: ['resources'] }),
    ]);
    onClose();
  };
  const save = useMutation({
    mutationFn: () =>
      api(term ? `/v1/terms/${term.id}` : `/v1/term-sets/${set.id}/terms`, {
        method: term ? 'PUT' : 'POST',
        ifMatch: term?.version,
        body: {
          name: name.trim(),
          description,
          color: color || null,
          synonyms: synonyms
            .split(',')
            .map((value) => value.trim())
            .filter(Boolean),
          labels: term?.labels ?? {},
          sort_order: term?.sort_order ?? 0,
          deprecated,
          available_as_keyword: keyword,
          parent_id: term ? undefined : parentId,
        },
      }),
    onSuccess: done,
  });
  const move = useMutation({
    mutationFn: () =>
      api(`/v1/terms/${term!.id}/move`, {
        method: 'POST',
        ifMatch: term!.version,
        body: { parent_id: parent.trim() || null },
      }),
    onSuccess: done,
  });
  const merge = useMutation({
    mutationFn: () =>
      api(`/v1/terms/${term!.id}/merge`, {
        method: 'POST',
        ifMatch: term!.version,
        body: { target_term_id: target.trim() },
      }),
    onSuccess: done,
  });
  return (
    <FormDialog
      open
      title={term ? `Term ${term.name}` : 'New term'}
      submitLabel="Save term"
      pending={save.isPending || move.isPending || merge.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Field id="term-name" label="Name">
        <Input id="term-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field id="term-synonyms" label="Synonyms">
        <Input
          id="term-synonyms"
          value={synonyms}
          onChange={(event) => setSynonyms(event.target.value)}
          placeholder="Comma separated"
        />
      </Field>
      <Field id="term-description" label="Description">
        <Textarea id="term-description" value={description} onChange={(event) => setDescription(event.target.value)} />
      </Field>
      <Field id="term-color" label="Color">
        <Input
          id="term-color"
          value={color}
          onChange={(event) => setColor(event.target.value)}
          placeholder="#4f46e5 or empty"
        />
      </Field>
      <label className="flex gap-2">
        <Checkbox checked={deprecated} onChange={(event) => setDeprecated(event.target.checked)} />
        Deprecated
      </label>
      <label className="flex gap-2">
        <Checkbox checked={keyword} onChange={(event) => setKeyword(event.target.checked)} />
        Available as a keyword
      </label>
      {term && (
        <>
          <Field id="term-parent" label="Move below term">
            <Input
              id="term-parent"
              value={parent}
              onChange={(event) => setParent(event.target.value)}
              placeholder="Parent term id, empty for root"
            />
          </Field>
          <Button disabled={move.isPending} onClick={() => move.mutate()}>
            Move term
          </Button>
          <Field id="term-target" label="Merge into term">
            <Input
              id="term-target"
              value={target}
              onChange={(event) => setTarget(event.target.value)}
              placeholder="Target term id"
            />
          </Field>
          <Button disabled={!target.trim() || merge.isPending} onClick={() => merge.mutate()}>
            Merge term and its references
          </Button>
        </>
      )}
    </FormDialog>
  );
}

function CSVImport({
  groupId,
  onClose,
  onSaved,
}: {
  groupId: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [csv, setCSV] = useState('');
  const upload = useMutation({
    mutationFn: () =>
      api<{ sets_created: number; terms_created: number }>(`/v1/term-groups/${groupId}/import`, {
        method: 'POST',
        raw: csv,
        headers: { 'Content-Type': 'text/csv' },
      }),
    onSuccess: async (result) => {
      toast.success(`${result.data.terms_created} terms and ${result.data.sets_created} sets imported`);
      await onSaved();
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title="Import term CSV"
      description="SharePoint term-set format: Term Set Name, Term Set Description, LCID, Available for Tagging, Term Description and Level 1 Term through Level 7 Term."
      submitLabel="Import"
      pending={upload.isPending}
      submitDisabled={!csv.trim()}
      onClose={onClose}
      onSubmit={() => upload.mutate()}
    >
      <Textarea aria-label="Term CSV" rows={10} value={csv} onChange={(event) => setCSV(event.target.value)} />
    </FormDialog>
  );
}

function KeywordPromotion({ sets, canEdit }: { sets: TermSet[]; canEdit: boolean }) {
  const client = useQueryClient();
  const popular = useQuery({
    queryKey: ['termStore', 'popular'],
    queryFn: async () =>
      (await api<{ value: { term: ManagedTerm; count: number }[] }>('/v1/keywords/popular')).data.value,
  });
  const [target, setTarget] = useState('');
  const promote = useMutation({
    mutationFn: (term: ManagedTerm) =>
      api(`/v1/keywords/${term.id}/promote`, { method: 'POST', ifMatch: term.version, body: { term_set_id: target } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['termStore'] }),
  });
  return (
    <div className="mt-4 border-t pt-3">
      <p className="mb-2 font-medium">Popular keywords</p>
      <QueryFeedback query={popular} />
      {canEdit && (
        <Select aria-label="Promote into term set" value={target} onChange={(event) => setTarget(event.target.value)}>
          <option value="">Target set…</option>
          {sets.map((set) => (
            <option key={set.id} value={set.id}>
              {set.name}
            </option>
          ))}
        </Select>
      )}
      <ul>
        {popular.data?.map(({ term, count }) => (
          <li key={term.id} className="flex items-center gap-3 py-2">
            <span className="flex-1">
              {term.name} · {count} uses
            </span>
            {canEdit && (
              <Button size="sm" disabled={!target || promote.isPending} onClick={() => promote.mutate(term)}>
                Promote
              </Button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
