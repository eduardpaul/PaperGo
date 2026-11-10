import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { downloadFile } from '@/api/files';
import { api } from '@/api/http';
import { collectionChildren } from '@/api/queries';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input, Textarea } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { useAccess } from '@/features/lists/access';
import { ConfirmDialog, SettingRow, SettingsSection } from '@/features/settings/section';
import {
  noChoices,
  optionsOf,
  packagePath,
  sendSolution,
  solutionKeys,
  solutionStatusQuery,
  solutionsQuery,
  type Choices,
  type Decision,
  type SettingSchema,
  type SolutionResult,
  type SolutionSlot,
  type SolutionSummary,
} from '@/features/solutions/api';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/solutions')({ component: SolutionsPage });

function SolutionsPage() {
  const { workspaceId } = Route.useParams();
  const { t } = useTranslation(['solutions', 'common']);
  const client = useQueryClient();
  const list = useQuery(solutionsQuery(workspaceId));
  const { canManage, loaded } = useAccess(workspaceId);
  const hasScope = useHasScope('solution.manage');
  const canWrite = canManage && hasScope;
  const [removing, setRemoving] = useState<SolutionSummary>();
  const remove = useMutation({
    mutationFn: (entry: SolutionSummary) =>
      api(`/v1/workspaces/${workspaceId}/solutions/${encodeURIComponent(entry.key)}`, { method: 'DELETE' }),
    onSuccess: async () => {
      setRemoving(undefined);
      await client.invalidateQueries({ queryKey: solutionKeys.list(workspaceId) });
    },
  });
  return (
    <SettingsSection title={t('page.title')} description={t('page.description')}>
      {loaded && !canWrite && <Alert tone="warning">{t('page.needScope')}</Alert>}
      <QueryFeedback query={list} />
      {list.data?.length === 0 && <p className="text-sm text-muted">{t('page.empty')}</p>}
      <div className="grid gap-4">
        {(list.data ?? []).map((entry) => (
          <Installed key={entry.key} workspaceId={workspaceId} entry={entry} canWrite={canWrite} onRemove={() => setRemoving(entry)} />
        ))}
      </div>
      {canWrite && <Install workspaceId={workspaceId} />}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(value) => !value && setRemoving(undefined)}
        title={t('page.removeTitle', { name: removing?.name ?? '' })}
        description={t('page.removeDescription')}
        confirm={t('page.removeConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => removing && remove.mutate(removing)}
      />
    </SettingsSection>
  );
}

/** One installed solution: its version, whether it has what it needs, and what can be done with it. */
function Installed({
  workspaceId,
  entry,
  canWrite,
  onRemove,
}: {
  workspaceId: string;
  entry: SolutionSummary;
  canWrite: boolean;
  onRemove: () => void;
}) {
  const { t } = useTranslation(['solutions', 'common']);
  const format = useFormat();
  const status = useQuery(solutionStatusQuery(workspaceId, entry.key));
  const download = useMutation({ mutationFn: () => downloadFile(packagePath(workspaceId, entry.key), `${entry.key}-${entry.version}.zip`) });
  return (
    <article className="grid gap-2 rounded-lg border p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="flex-1 text-sm font-medium">{entry.name}</h3>
        {entry.paused && <Badge>{t('page.paused')}</Badge>}
        {status.data && <Badge>{status.data.ok ? t('status.ok') : t('status.attention')}</Badge>}
      </div>
      <p className="text-xs text-muted">
        {t('page.version', { key: entry.key, version: entry.version })} ·{' '}
        {t('page.applied', { date: format.dateTime(entry.applied_at), who: entry.applied_by })}
      </p>
      {status.data && !status.data.ok && (
        <Alert tone="warning">
          <ul className="list-disc pl-4">
            {status.data.problems.map((problem) => (
              <li key={problem}>{problem}</li>
            ))}
          </ul>
        </Alert>
      )}
      <QueryFeedback query={download} />
      <div className="flex flex-wrap gap-2">
        <Button size="sm" disabled={download.isPending} onClick={() => download.mutate()}>
          {t('actions.download')}
        </Button>
        {canWrite && (
          <Button size="sm" variant="danger" onClick={onRemove}>
            {t('actions.remove')}
          </Button>
        )}
      </div>
    </article>
  );
}

/** Installing or updating: choose the packed solution, review what applying does, choose, apply. */
function Install({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation(['solutions', 'common']);
  const client = useQueryClient();
  const [file, setFile] = useState<File>();
  const [choices, setChoices] = useState<Choices>(noChoices);
  const [result, setResult] = useState<SolutionResult>();
  // The choices the shown result was computed with: Apply waits for a review of newer ones.
  const [reviewed, setReviewed] = useState('');
  const collections = useQuery({
    queryKey: ['solutions', workspaceId, 'collections'],
    queryFn: () => collectionChildren(workspaceId),
    enabled: !!file,
  });
  const send = useMutation({
    mutationFn: (step: 'plan' | 'apply') => sendSolution(workspaceId, file!, step, optionsOf(choices)),
    onSuccess: async (next, step) => {
      // The first review brings the values the solution would use, to change from there.
      const settled: Choices = {
        ...choices,
        settings: { ...next.settings, ...choices.settings },
        bindings: {
          ...Object.fromEntries(next.slots.filter((slot) => slot.state === 'bound').map((slot) => [slot.key, slot.collection_id ?? ''])),
          ...choices.bindings,
        },
      };
      setChoices(settled);
      setReviewed(JSON.stringify(settled));
      setResult(next);
      if (step === 'apply' && next.applied) {
        await client.invalidateQueries({ queryKey: solutionKeys.list(workspaceId) });
      }
    },
  });
  const choose = (next: File | undefined) => {
    setFile(next);
    setChoices(noChoices);
    setResult(undefined);
    setReviewed('');
    send.reset();
  };
  const change = (patch: Partial<Choices>) => setChoices((current) => ({ ...current, ...patch }));
  const stale = !!result && JSON.stringify(choices) !== reviewed;
  const undecided = (result?.refusals ?? []).some((refusal) => refusal.conflict && !choices.decisions[`${refusal.kind}:${refusal.key}`]);
  const blocked = (result?.refusals ?? []).some((refusal) => !refusal.conflict);
  return (
    <section className="grid gap-3 rounded-lg border p-4">
      <div>
        <h3 className="text-sm font-medium">{t('install.title')}</h3>
        <p className="text-xs text-muted">{t('install.description')}</p>
      </div>
      <SettingRow id="solution-file" label={t('install.file')}>
        <Input
          id="solution-file"
          type="file"
          accept=".zip,application/zip"
          onChange={(event) => choose(event.target.files?.[0])}
        />
      </SettingRow>
      {file && !result && <p className="text-xs text-muted">{t('install.reviewFirst')}</p>}
      {result && (
        <Plan
          result={result}
          choices={choices}
          change={change}
          collections={(collections.data ?? []).map((c) => ({ id: c.id, name: c.name, kind: c.kind }))}
        />
      )}
      <QueryFeedback query={send} />
      {result?.applied && !stale && <Alert tone="success">{t('plan.applied', { count: result.changes.length })}</Alert>}
      {stale && <Alert tone="warning">{t('plan.stale')}</Alert>}
      {file && (
        <div className="flex flex-wrap gap-2">
          <Button disabled={send.isPending} onClick={() => send.mutate('plan')}>
            {t('actions.review')}
          </Button>
          <Button
            variant="primary"
            disabled={send.isPending || !result || result.applied || stale || blocked || undecided}
            onClick={() => send.mutate('apply')}
          >
            {t('actions.apply')}
          </Button>
        </div>
      )}
    </section>
  );
}

function Plan({
  result,
  choices,
  change,
  collections,
}: {
  result: SolutionResult;
  choices: Choices;
  change: (patch: Partial<Choices>) => void;
  collections: { id: string; name: string; kind: string }[];
}) {
  const { t } = useTranslation(['solutions', 'common']);
  const settings = Object.entries(result.settings_form?.properties ?? {});
  const kind = (value: string) => t(`kind.${value}` as 'kind.field', { defaultValue: value });
  return (
    <div className="grid gap-3">
      <p className="text-sm font-medium">
        {t('plan.heading', { name: result.name, version: result.version })}{' '}
        <span className="font-normal text-muted">· {result.from ? t('plan.from', { version: result.from }) : t('plan.first')}</span>
      </p>

      {result.languages.length > 0 && (
        <SettingRow id="solution-language" label={t('plan.language')} hint={t('plan.languageHint')}>
          <Select id="solution-language" value={choices.language} onChange={(event) => change({ language: event.target.value })}>
            <option value="">{result.language ? t('plan.languageKeep', { language: result.language }) : t('plan.languageOwn')}</option>
            {result.languages.map((language) => (
              <option key={language} value={language}>
                {language}
              </option>
            ))}
            <option value="none">{t('plan.languageNone')}</option>
          </Select>
        </SettingRow>
      )}

      {settings.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-muted uppercase">{t('plan.settings')}</h4>
          {settings.map(([name, schema]) => (
            <SettingRow key={name} id={`setting-${name}`} label={schema.title ?? name} hint={schema.description}>
              <SettingInput
                id={`setting-${name}`}
                schema={schema}
                value={choices.settings[name]}
                onChange={(value) => change({ settings: { ...choices.settings, [name]: value } })}
              />
            </SettingRow>
          ))}
        </div>
      )}

      {result.slots.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-muted uppercase">{t('plan.collections')}</h4>
          {result.slots.map((slot) => (
            <SettingRow
              key={slot.key}
              id={`slot-${slot.key}`}
              label={`${slot.name ?? slot.key} (${slot.kind === 'library' ? t('plan.kindLibrary') : t('plan.kindList')})`}
            >
              <SlotChoice
                slot={slot}
                value={choices.bindings[slot.key] ?? ''}
                collections={collections.filter((c) => c.kind === slot.kind)}
                onChange={(id) => change({ bindings: { ...choices.bindings, [slot.key]: id } })}
              />
            </SettingRow>
          ))}
        </div>
      )}

      <div>
        <h4 className="text-xs font-medium text-muted uppercase">{t('plan.changes')}</h4>
        {result.changes.length === 0 && result.refusals.length === 0 && <p className="text-sm text-muted">{t('plan.noChanges')}</p>}
        <ul className="grid gap-0.5 text-sm">
          {result.changes.map((entry) => (
            <li key={`${entry.action}-${entry.kind}-${entry.key}-${entry.detail ?? ''}`}>
              <span className="font-medium">{t(`action.${entry.action}`)}</span> {kind(entry.kind)} <code>{entry.key}</code>
              {entry.detail && <span className="text-muted"> · {entry.detail}</span>}
            </li>
          ))}
        </ul>
      </div>

      {result.customized.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-muted uppercase">{t('plan.kept')}</h4>
          <ul className="text-sm">
            {result.customized.map((name) => (
              <li key={name}>
                {kind(name.slice(0, name.indexOf(':')))} <code>{name.slice(name.indexOf(':') + 1)}</code>
              </li>
            ))}
          </ul>
        </div>
      )}

      {result.warnings.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-muted uppercase">{t('plan.warnings')}</h4>
          <ul className="list-disc pl-4 text-sm text-muted">
            {result.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </div>
      )}

      {result.refusals.length > 0 && (
        <Alert tone="danger">
          <p className="font-medium">{t('plan.refusals')}</p>
          <ul className="mt-1 grid gap-2">
            {result.refusals.map((refusal) => {
              const name = `${refusal.kind}:${refusal.key}`;
              return (
                <li key={`${name}-${refusal.reason}`}>
                  {kind(refusal.kind)} <code>{refusal.key}</code>: {refusal.reason}
                  {refusal.conflict && (
                    <Select
                      className="mt-1 max-w-xs"
                      aria-label={t('plan.decide')}
                      value={choices.decisions[name] ?? ''}
                      onChange={(event) => {
                        const decisions = { ...choices.decisions };
                        if (event.target.value) decisions[name] = event.target.value as Decision;
                        else delete decisions[name];
                        change({ decisions });
                      }}
                    >
                      <option value="">{t('plan.undecided')}</option>
                      <option value="keep">{t('plan.keep')}</option>
                      <option value="replace">{t('plan.replace')}</option>
                    </Select>
                  )}
                </li>
              );
            })}
          </ul>
        </Alert>
      )}

      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={choices.paused ?? result.paused} onChange={(event) => change({ paused: event.target.checked })} /> {t('plan.paused')}
      </label>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={choices.prune} onChange={(event) => change({ prune: event.target.checked })} /> {t('plan.prune')}
      </label>
    </div>
  );
}

/** The input of one setting of the solution's settings form, by its type. */
function SettingInput({
  id,
  schema,
  value,
  onChange,
}: {
  id: string;
  schema: SettingSchema;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  if (schema.type === 'boolean') {
    return <Checkbox id={id} checked={value === true} onChange={(event) => onChange(event.target.checked)} />;
  }
  if (schema.enum) {
    return (
      <Select id={id} value={String(value ?? '')} onChange={(event) => onChange(event.target.value)}>
        {schema.enum.map((option) => (
          <option key={String(option)} value={String(option)}>
            {String(option)}
          </option>
        ))}
      </Select>
    );
  }
  if (schema.type === 'number' || schema.type === 'integer') {
    return (
      <Input
        id={id}
        type="number"
        value={value === undefined || value === null ? '' : String(value)}
        onChange={(event) => onChange(event.target.value === '' ? undefined : Number(event.target.value))}
      />
    );
  }
  if (schema.type === 'array' || schema.type === 'object') {
    return <JSONInput id={id} value={value} onChange={onChange} />;
  }
  return <Input id={id} value={typeof value === 'string' ? value : ''} onChange={(event) => onChange(event.target.value)} />;
}

/** A list or object setting, edited as JSON; it changes the setting while it parses. */
function JSONInput({ id, value, onChange }: { id: string; value: unknown; onChange: (value: unknown) => void }) {
  const [text, setText] = useState(() => JSON.stringify(value ?? null));
  return (
    <Textarea
      id={id}
      rows={2}
      className="font-mono text-xs"
      value={text}
      onChange={(event) => {
        setText(event.target.value);
        try {
          onChange(JSON.parse(event.target.value));
        } catch {
          /* Not JSON yet: the setting keeps its last value. */
        }
      }}
    />
  );
}

/** What stands for one of the solution's collections: created, an existing one, or another solution's. */
function SlotChoice({
  slot,
  value,
  collections,
  onChange,
}: {
  slot: SolutionSlot;
  value: string;
  collections: { id: string; name: string }[];
  onChange: (id: string) => void;
}) {
  const { t } = useTranslation('solutions');
  if (slot.state === 'installed') return <p className="pt-2 text-sm">{t('plan.slotInstalled', { name: slot.collection_name ?? '' })}</p>;
  if (slot.uses) {
    return (
      <p className="pt-2 text-sm">
        {slot.state === 'uses'
          ? t('plan.slotUses', { name: slot.collection_name ?? '', solution: slot.uses })
          : t('plan.slotUsesMissing', { solution: slot.uses })}
      </p>
    );
  }
  return (
    <Select id={`slot-${slot.key}`} value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="">{slot.owned ? t('plan.slotCreate', { name: slot.name ?? slot.key }) : t('plan.slotChoose')}</option>
      {collections.map((collection) => (
        <option key={collection.id} value={collection.id}>
          {collection.name}
        </option>
      ))}
    </Select>
  );
}
