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
import { useTranslation } from '@/i18n';
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
  const { t } = useTranslation(['admin', 'common']);
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
  if (!canRead) return <Alert>{t('terms.needRead')}</Alert>;
  return (
    <div className="flex flex-col gap-5">
      <div className="grid gap-5 lg:grid-cols-[18rem_1fr]">
        <SettingsSection
          title={t('terms.sets')}
          actions={
            canEdit && (
              <Button onClick={() => setEditing({ kind: 'group' })}>
                <Plus /> {t('terms.group')}
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
                        aria-label={t('terms.edit', { name: group.name })}
                        onClick={() => setEditing({ kind: 'group', entity: group })}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t('terms.newSetIn', { name: group.name })}
                        onClick={() => setEditing({ kind: 'set', groupId: group.id })}
                      >
                        <Plus />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t('terms.delete', { name: group.name })}
                        onClick={() => setDeleting({ kind: 'group', entity: group })}
                      >
                        <Trash2 />
                      </Button>
                      <Button size="sm" onClick={() => setImportGroup(group.id)}>
                        {t('terms.importCsv')}
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
                          {set.is_keywords && <Badge className="ms-2">{t('terms.keywords')}</Badge>}
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
            description={selected.is_open ? t('terms.open') : t('terms.managed')}
            actions={
              canEdit &&
              !selected.is_keywords && (
                <>
                  <Button onClick={() => setEditing({ kind: 'set', entity: selected, groupId: selected.group_id })}>
                    {t('terms.editSet')}
                  </Button>
                  <Button variant="danger" onClick={() => setDeleting({ kind: 'set', entity: selected })}>
                    {t('terms.deleteSet')}
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
            title={t('terms.choose')}
            description={t('terms.chooseDescription')}
          />
        )}
      </div>
      <PackageTransfer kind="taxonomy" path="/v1/taxonomy" canImport={canEdit} onImported={refresh} />
      {editing && <TaxonomyNameDialog {...editing} onClose={() => setEditing(undefined)} onSaved={refresh} />}
      {importGroup && <CSVImport groupId={importGroup} onClose={() => setImportGroup('')} onSaved={refresh} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={t('terms.deleteTitle', { name: deleting?.entity.name ?? '' })}
        description={t('terms.deleteDescription')}
        confirm={t('common:actions.delete')}
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
  const { t } = useTranslation(['admin', 'common']);
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
      title={
        kind === 'group'
          ? entity ? t('terms.dialog.editGroup') : t('terms.dialog.newGroup')
          : entity ? t('terms.dialog.editSet') : t('terms.dialog.newSet')
      }
      submitLabel={t('common:actions.save')}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Field id="taxonomy-name" label={t('common:fields.name')}>
        <Input id="taxonomy-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      {kind === 'set' && (
        <Field id="taxonomy-key" label={t('terms.key')}>
          <Input
            id="taxonomy-key"
            required
            disabled={!!entity}
            value={key}
            onChange={(event) => setKey(event.target.value)}
          />
        </Field>
      )}
      <Field id="taxonomy-description" label={t('common:fields.description')}>
        <Textarea
          id="taxonomy-description"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </Field>
      {kind === 'set' && (
        <label className="flex gap-2">
          <Checkbox checked={open} onChange={(event) => setOpen(event.target.checked)} />
          {t('terms.allowAdd')}
        </label>
      )}
    </FormDialog>
  );
}

function TermLevel({ set, parentId, canEdit }: { set: TermSet; parentId?: string; canEdit: boolean }) {
  const { t } = useTranslation('admin');
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
                aria-label={t('terms.expand', { name: term.name })}
                disabled={!term.has_children}
                onClick={() => setExpanded(expanded === term.id ? '' : term.id)}
              >
                <ChevronRight />
              </Button>
              <span className="flex-1" style={{ color: term.color || undefined }}>
                {term.name} {term.deprecated && <Badge>{t('terms.deprecated')}</Badge>}
              </span>
              {canEdit && (
                <Button size="icon" variant="ghost" aria-label={t('terms.edit', { name: term.name })} onClick={() => setEditing(term)}>
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
          <Plus /> {parentId ? t('terms.newTermBelow') : t('terms.newTerm')}
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
  const { t } = useTranslation(['admin', 'common']);
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
      title={term ? t('terms.term', { name: term.name }) : t('terms.newTerm')}
      submitLabel={t('terms.saveTerm')}
      pending={save.isPending || move.isPending || merge.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Field id="term-name" label={t('common:fields.name')}>
        <Input id="term-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field id="term-synonyms" label={t('terms.synonyms')}>
        <Input
          id="term-synonyms"
          value={synonyms}
          onChange={(event) => setSynonyms(event.target.value)}
          placeholder={t('terms.commaSeparated')}
        />
      </Field>
      <Field id="term-description" label={t('common:fields.description')}>
        <Textarea id="term-description" value={description} onChange={(event) => setDescription(event.target.value)} />
      </Field>
      <Field id="term-color" label={t('terms.color')}>
        <Input
          id="term-color"
          value={color}
          onChange={(event) => setColor(event.target.value)}
          placeholder={t('terms.colorPlaceholder')}
        />
      </Field>
      <label className="flex gap-2">
        <Checkbox checked={deprecated} onChange={(event) => setDeprecated(event.target.checked)} />
        {t('terms.deprecated')}
      </label>
      <label className="flex gap-2">
        <Checkbox checked={keyword} onChange={(event) => setKeyword(event.target.checked)} />
        {t('terms.availableAsKeyword')}
      </label>
      {term && (
        <>
          <Field id="term-parent" label={t('terms.moveBelow')}>
            <Input
              id="term-parent"
              value={parent}
              onChange={(event) => setParent(event.target.value)}
              placeholder={t('terms.parentPlaceholder')}
            />
          </Field>
          <Button disabled={move.isPending} onClick={() => move.mutate()}>
            {t('terms.move')}
          </Button>
          <Field id="term-target" label={t('terms.mergeInto')}>
            <Input
              id="term-target"
              value={target}
              onChange={(event) => setTarget(event.target.value)}
              placeholder={t('terms.targetPlaceholder')}
            />
          </Field>
          <Button disabled={!target.trim() || merge.isPending} onClick={() => merge.mutate()}>
            {t('terms.merge')}
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
  const { t } = useTranslation('admin');
  const [csv, setCSV] = useState('');
  const upload = useMutation({
    mutationFn: () =>
      api<{ sets_created: number; terms_created: number }>(`/v1/term-groups/${groupId}/import`, {
        method: 'POST',
        raw: csv,
        headers: { 'Content-Type': 'text/csv' },
      }),
    onSuccess: async (result) => {
      toast.success(
        t('terms.imported', {
          terms: t('terms.termCount', { count: result.data.terms_created }),
          sets: t('terms.setCount', { count: result.data.sets_created }),
        }),
      );
      await onSaved();
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={t('terms.csvTitle')}
      description={t('terms.csvDescription')}
      submitLabel={t('terms.import')}
      pending={upload.isPending}
      submitDisabled={!csv.trim()}
      onClose={onClose}
      onSubmit={() => upload.mutate()}
    >
      <Textarea aria-label={t('terms.csv')} rows={10} value={csv} onChange={(event) => setCSV(event.target.value)} />
    </FormDialog>
  );
}

function KeywordPromotion({ sets, canEdit }: { sets: TermSet[]; canEdit: boolean }) {
  const { t } = useTranslation('admin');
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
      <p className="mb-2 font-medium">{t('terms.popular')}</p>
      <QueryFeedback query={popular} />
      {canEdit && (
        <Select aria-label={t('terms.promoteInto')} value={target} onChange={(event) => setTarget(event.target.value)}>
          <option value="">{t('terms.targetSet')}</option>
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
              {t('terms.uses', { name: term.name, count })}
            </span>
            {canEdit && (
              <Button size="sm" disabled={!target || promote.isPending} onClick={() => promote.mutate(term)}>
                {t('terms.promote')}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
