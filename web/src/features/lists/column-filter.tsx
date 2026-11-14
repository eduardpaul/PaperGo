import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Funnel, FunnelPlus, FunnelX, X } from 'lucide-react';
import { useId, useState, type FormEvent } from 'react';
import { api } from '@/api/http';
import { typedNumberText } from '@/api/json';
import { keywords } from '@/api/queries';
import { andFilter } from '@/api/requests';
import type { FieldDefinition, Page, QueryGroup, QuerySpec } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Alert, Spinner } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Checkbox, Select } from '@/components/ui/select';
import { FieldEditor } from '@/features/fields/editor';
import { useResourceNames, useTermNames } from '@/features/fields/names';
import { usePeopleNames } from '@/features/fields/people';
import { asList } from '@/features/fields/values';
import { useTranslation } from '@/i18n';
import { decimalSeparator } from '@/lib/format';
import { useFormat } from '@/lib/preferences';
import { useDebounced } from '@/lib/use-debounced';
import { cn } from '@/lib/utils';
import {
  canPickEmpty,
  filterability,
  monthRange,
  offersSubterms,
  periodRange,
  periods,
  usableFilter,
  type ColumnFilter,
  type ColumnFilterOp,
  type ColumnFilters,
  type Filterability,
  type Filterable,
  type FilterValue,
  type Period,
} from './column-filters';
import { columnLabel } from './schema';

/** What a page's column filters need: the collection, its fields, the filters and how to change them. */
export interface ColumnFiltering {
  collectionId: string;
  fields: FieldDefinition[];
  filters?: ColumnFilters;
  /** Sets or removes a column's filter; false when the query would grow past what the API takes. */
  apply: (column: string, filter: ColumnFilter | undefined) => boolean;
  /** What the page shows without the column's own filter: the items whose values a value list offers. */
  valuesQuery: (column: string) => QuerySpec;
}

const popoverClass = 'w-[min(20rem,calc(100vw-2rem))] p-3';

/** A column's filter on its table header: a funnel that is marked while the column is filtered. */
export function ColumnFilterButton({ filtering, column, label }: { filtering: ColumnFiltering; column: string; label: string }) {
  const { t } = useTranslation('lists');
  const [open, setOpen] = useState(false);
  const info = filterability(column, filtering.fields);
  const stored = filtering.filters?.[column];
  const active = !!stored && !!usableFilter(info, stored);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={active ? t('columnFilter.filtered', { column: label }) : t('columnFilter.filter', { column: label })}
          title={active ? t('columnFilter.filtered', { column: label }) : t('columnFilter.filter', { column: label })}
          className={cn(
            'inline-flex shrink-0 items-center rounded p-0.5 hover:bg-surface-muted hover:text-foreground focus-visible:opacity-100',
            active
              ? 'text-accent'
              : 'opacity-0 group-hover/header:opacity-60 group-focus-within/header:opacity-60 [@media(hover:none)]:opacity-60',
            open && 'opacity-100',
          )}
        >
          <Funnel className={cn('size-3.5', active && 'fill-current')} />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className={popoverClass}>
        <ColumnFilterForm filtering={filtering} column={column} label={label} info={info} onDone={() => setOpen(false)} />
      </PopoverContent>
    </Popover>
  );
}

/**
 * The filters in force, each a chip that edits it, with "Clear all" and, for people who may edit the view, saving
 * them into it.
 */
export function ColumnFilterBar({
  filtering,
  active,
  onClear,
  save,
}: {
  filtering: ColumnFiltering;
  active: [string, ColumnFilter][];
  onClear: () => void;
  /** Saving into the open view, for people who may edit it. */
  save?: { label: string; pending: boolean; error?: string; onSave: () => void };
}) {
  const { t } = useTranslation('lists');
  const summary = useFilterSummary(active, filtering.fields);
  if (active.length === 0) return null;
  return (
    <div role="group" aria-label={t('columnFilter.active')} className="mb-3 flex flex-wrap items-center gap-2">
      <ul className="flex flex-wrap items-center gap-2">
        {active.map(([column, filter]) => (
          <FilterChip key={column} filtering={filtering} column={column} text={summary(column, filter)} />
        ))}
      </ul>
      <Button size="sm" variant="ghost" onClick={onClear}>
        <FunnelX /> {t('columnFilter.clearAll')}
      </Button>
      {save && (
        <Button size="sm" disabled={save.pending} onClick={save.onSave}>
          {save.pending && <Spinner />} {save.label}
        </Button>
      )}
      {save?.error && <Alert className="w-full">{save.error}</Alert>}
    </div>
  );
}

function FilterChip({ filtering, column, text }: { filtering: ColumnFiltering; column: string; text: string }) {
  const { t } = useTranslation('lists');
  const [open, setOpen] = useState(false);
  const label = columnLabel(column, filtering.fields);
  return (
    <li className="flex max-w-full items-center rounded-full border border-accent/40 bg-accent-soft text-xs">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button type="button" className="max-w-[18rem] truncate rounded-l-full py-1 pr-1 pl-2.5 hover:underline" aria-label={t('columnFilter.edit', { filter: text })}>
            {text}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className={popoverClass}>
          <ColumnFilterForm filtering={filtering} column={column} label={label} info={filterability(column, filtering.fields)} onDone={() => setOpen(false)} />
        </PopoverContent>
      </Popover>
      <button
        type="button"
        className="rounded-r-full py-1 pr-2 pl-1 text-muted hover:text-foreground"
        aria-label={t('columnFilter.remove', { filter: text })}
        onClick={() => filtering.apply(column, undefined)}
      >
        <X className="size-3" />
      </button>
    </li>
  );
}

/** Filters any column, shown or not: for layouts without headers, and for columns the table does not show. */
export function AddFilterButton({ filtering, columns }: { filtering: ColumnFiltering; columns: { key: string; label: string }[] }) {
  const { t } = useTranslation('lists');
  const [open, setOpen] = useState(false);
  const [column, setColumn] = useState<string>();
  const chosen = columns.find((entry) => entry.key === column);
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setColumn(undefined);
      }}
    >
      <PopoverTrigger asChild>
        <Button size="sm" variant="ghost">
          <FunnelPlus /> {t('columnFilter.add')}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className={chosen ? popoverClass : 'w-[min(18rem,calc(100vw-2rem))] p-0'}>
        {chosen ? (
          <ColumnFilterForm
            filtering={filtering}
            column={chosen.key}
            label={chosen.label}
            info={filterability(chosen.key, filtering.fields)}
            onBack={() => setColumn(undefined)}
            onDone={() => {
              setOpen(false);
              setColumn(undefined);
            }}
          />
        ) : (
          <>
            <p className="border-b px-3 py-2 text-xs font-medium text-muted">{t('columnFilter.pickColumn')}</p>
            <ul className="max-h-72 overflow-y-auto p-1">
              {columns.map((entry) => {
                const info = filterability(entry.key, filtering.fields);
                return (
                  <li key={entry.key}>
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-surface-muted"
                      onClick={() => setColumn(entry.key)}
                    >
                      <span className={cn('flex-1 truncate', !info.kind && 'text-muted')}>{entry.label}</span>
                      {filtering.filters?.[entry.key] && <Funnel className="size-3.5 fill-current text-accent" aria-label={t('columnFilter.isFiltered')} />}
                      {!info.kind && <span className="text-xs text-muted">{t('columnFilter.notFilterable')}</span>}
                    </button>
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}

/** What the form edits: every way of filtering at once, so switching between them keeps what was typed. */
interface Draft {
  op: ColumnFilterOp;
  text: string;
  from: string;
  to: string;
  values: FilterValue[];
  empty: boolean;
  me: boolean;
  sub: boolean;
  exclude: boolean;
}

function draftOf(info: Filterability, filter: ColumnFilter | undefined, mark: string): Draft {
  const shown = (text = '') => (info.kind === 'number' && mark === ',' ? text.replace('.', ',') : text);
  const draft: Draft = { op: info.kind ? info.ops[0] : 'contains', text: '', from: '', to: '', values: [], empty: false, me: false, sub: false, exclude: false };
  if (!filter) return draft;
  draft.op = filter.op;
  if (filter.op === 'contains') draft.text = filter.text;
  if (filter.op === 'equals') draft.text = shown(filter.value);
  if (filter.op === 'range') {
    draft.from = shown(filter.from);
    draft.to = shown(filter.to);
  }
  if (filter.op === 'values') {
    draft.values = filter.values;
    draft.empty = !!filter.empty;
    draft.me = !!filter.me;
    draft.sub = !!filter.sub;
    draft.exclude = !!filter.exclude;
  }
  return draft;
}

type Invalid = 'invalidNumber' | 'invalidInteger';

/** The filter a draft makes, numbers as JSON number text; undefined when it filters nothing. */
function filterOf(info: Filterability, draft: Draft): { filter?: ColumnFilter; invalid?: Invalid } {
  if (!info.kind) return {};
  const integer = info.field.type === 'integer';
  const number = (text: string): string | undefined | false => {
    if (!text.trim()) return undefined;
    return typedNumberText(text, integer) ?? false;
  };
  const invalid: Invalid = integer ? 'invalidInteger' : 'invalidNumber';
  let filter: ColumnFilter;
  switch (draft.op) {
    case 'contains':
      filter = { op: 'contains', text: draft.text.trim() };
      break;
    case 'equals': {
      if (info.kind === 'number') {
        const value = number(draft.text);
        if (value === false) return { invalid };
        filter = { op: 'equals', value: value ?? '' };
      } else filter = { op: 'equals', value: info.kind === 'date' || info.kind === 'datetime' ? draft.text : draft.text.trim() };
      break;
    }
    case 'range': {
      const from = info.kind === 'number' ? number(draft.from) : draft.from || undefined;
      const to = info.kind === 'number' ? number(draft.to) : draft.to || undefined;
      if (from === false || to === false) return { invalid };
      filter = { op: 'range', ...(from ? { from } : {}), ...(to ? { to } : {}) };
      break;
    }
    case 'empty':
    case 'present':
      filter = { op: draft.op };
      break;
    case 'values':
      filter = {
        op: 'values',
        values: draft.values,
        ...(draft.empty ? { empty: true as const } : {}),
        ...(draft.me ? { me: true as const } : {}),
        ...(draft.sub ? { sub: true as const } : {}),
        ...(draft.exclude ? { exclude: true as const } : {}),
      };
      break;
  }
  return { filter: usableFilter(info, filter) };
}

/** Filters one column in the way its kind of value allows, or says why it cannot be filtered. */
export function ColumnFilterForm({
  filtering,
  column,
  label,
  info,
  onDone,
  onBack,
}: {
  filtering: ColumnFiltering;
  column: string;
  label: string;
  info: Filterability;
  onDone: () => void;
  onBack?: () => void;
}) {
  const { t } = useTranslation(['lists', 'common', 'home']);
  const format = useFormat();
  const id = useId();
  const mark = decimalSeparator(format.preferences.numberFormat);
  const stored = filtering.filters?.[column];
  const current = stored && usableFilter(info, stored);
  const [draft, setDraft] = useState(() => draftOf(info, current, mark));
  const [problem, setProblem] = useState<string>();
  const patch = (next: Partial<Draft>) => {
    setProblem(undefined);
    setDraft((previous) => ({ ...previous, ...next }));
  };
  const heading = (
    <div className="mb-2 flex items-center gap-2">
      {onBack && (
        <Button size="sm" variant="ghost" className="-ml-1 px-1.5" onClick={onBack}>
          {t('common:actions.back')}
        </Button>
      )}
      <h3 id={`${id}-title`} className="flex-1 truncate text-[13px] font-semibold">
        {t('columnFilter.title', { column: label })}
      </h3>
    </div>
  );
  if (!info.kind) {
    return (
      <div aria-labelledby={`${id}-title`} role="group">
        {heading}
        <p className="text-[13px] text-muted">{t(`columnFilter.reason.${info.reason}`)}</p>
      </div>
    );
  }
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const { filter, invalid } = filterOf(info, draft);
    if (invalid) {
      setProblem(t(`home:values.${invalid}`));
      return;
    }
    if (!filtering.apply(column, filter)) {
      setProblem(t('columnFilter.tooMany'));
      return;
    }
    onDone();
  };
  const dates = info.kind === 'date' || info.kind === 'datetime';
  const numeric = info.kind === 'number';
  return (
    <form aria-labelledby={`${id}-title`} className="grid gap-3" onSubmit={submit}>
      {heading}
      {info.ops.length > 1 && (
        <Select aria-label={t('columnFilter.condition')} value={draft.op} onChange={(event) => patch({ op: event.target.value as ColumnFilterOp })}>
          {info.ops.map((op) => (
            <option key={op} value={op}>
              {t(`columnFilter.ops.${op}`)}
            </option>
          ))}
        </Select>
      )}
      {(draft.op === 'contains' || (draft.op === 'equals' && !dates)) && (
        <Input
          autoFocus
          aria-label={draft.op === 'contains' ? t('columnFilter.text') : t('columnFilter.value')}
          value={draft.text}
          inputMode={numeric ? 'decimal' : undefined}
          onChange={(event) => patch({ text: event.target.value })}
        />
      )}
      {draft.op === 'equals' && dates && (
        <Input autoFocus type="date" aria-label={t('columnFilter.day')} value={draft.text} onChange={(event) => patch({ text: event.target.value })} />
      )}
      {draft.op === 'range' && (
        <div className="grid gap-2">
          {dates && (
            <div className="grid grid-cols-2 gap-2">
              <Select
                aria-label={t('columnFilter.period')}
                value=""
                onChange={(event) => {
                  if (!event.target.value) return;
                  const range = periodRange(event.target.value as Period, format.dayKey(new Date()));
                  patch(range);
                }}
              >
                <option value="">{t('columnFilter.period')}</option>
                {periods.map((period) => (
                  <option key={period} value={period}>
                    {t(`columnFilter.periods.${period}`)}
                  </option>
                ))}
              </Select>
              <Input
                type="month"
                aria-label={t('columnFilter.month')}
                value={/^\d{4}-\d{2}-01$/.test(draft.from) && monthRange(draft.from.slice(0, 7)).to === draft.to ? draft.from.slice(0, 7) : ''}
                onChange={(event) => event.target.value && patch(monthRange(event.target.value))}
              />
            </div>
          )}
          <div className="grid grid-cols-2 gap-2">
            <div className="grid gap-1">
              <Label htmlFor={`${id}-from`}>{t('columnFilter.from')}</Label>
              <Input
                id={`${id}-from`}
                autoFocus={!dates}
                type={dates ? 'date' : 'text'}
                inputMode={numeric ? 'decimal' : undefined}
                value={draft.from}
                onChange={(event) => patch({ from: event.target.value })}
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor={`${id}-to`}>{t('columnFilter.to')}</Label>
              <Input
                id={`${id}-to`}
                type={dates ? 'date' : 'text'}
                inputMode={numeric ? 'decimal' : undefined}
                value={draft.to}
                onChange={(event) => patch({ to: event.target.value })}
              />
            </div>
          </div>
          <p className="text-xs text-muted">{dates ? t('columnFilter.rangeDaysHint') : t('columnFilter.rangeHint')}</p>
        </div>
      )}
      {draft.op === 'values' && <ValuesEditor id={id} filtering={filtering} column={column} info={info} draft={draft} patch={patch} />}
      {problem && <Alert>{problem}</Alert>}
      <div className="flex flex-wrap justify-end gap-2">
        {current && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              filtering.apply(column, undefined);
              onDone();
            }}
          >
            {t('columnFilter.clear')}
          </Button>
        )}
        <Button size="sm" type="submit" variant="primary">
          {t('common:actions.apply')}
        </Button>
      </div>
    </form>
  );
}

/** Picks the values a column must (or must not) hold, in the picker its kind of value needs. */
function ValuesEditor({
  id,
  filtering,
  column,
  info,
  draft,
  patch,
}: {
  id: string;
  filtering: ColumnFiltering;
  column: string;
  info: Filterable;
  draft: Draft;
  patch: (next: Partial<Draft>) => void;
}) {
  const { t } = useTranslation(['lists', 'common']);
  const toggle = (value: FilterValue, on: boolean) =>
    patch({ values: on ? [...draft.values.filter((entry) => entry !== value), value] : draft.values.filter((entry) => entry !== value) });
  const values = draft.values.filter((value): value is string => typeof value === 'string');
  const picker =
    info.kind === 'choice' ? (
      <CheckList label={t('columnFilter.values')} options={(info.field.choices ?? []).map((choice) => ({ value: choice, label: choice }))} selected={draft.values} onToggle={toggle} />
    ) : info.kind === 'boolean' ? (
      <CheckList
        label={t('columnFilter.values')}
        options={[
          { value: true, label: t('common:answer.yes') },
          { value: false, label: t('common:answer.no') },
        ]}
        selected={draft.values}
        onToggle={toggle}
      />
    ) : info.kind === 'text' ? (
      <TextValues filtering={filtering} column={column} selected={values} onToggle={toggle} />
    ) : info.kind === 'keywords' ? (
      <div className="grid gap-1">
        <Label id={`${id}-values-label`}>{t('columnFilter.values')}</Label>
        <KeywordValues id={`${id}-values`} selected={values} onChange={(next) => patch({ values: next })} />
      </div>
    ) : (
      <FieldEditor
        id={`${id}-values`}
        field={{ ...info.field, label: t('columnFilter.values'), required: false, options: { ...info.field.options, multiple: true, description: undefined } }}
        value={values}
        onChange={(next) => patch({ values: asList(next) })}
      />
    );
  return (
    <div className="grid gap-2">
      <Select aria-label={t('columnFilter.match')} value={draft.exclude ? 'none' : 'any'} onChange={(event) => patch({ exclude: event.target.value === 'none' })}>
        <option value="any">{t('columnFilter.anyOf')}</option>
        <option value="none">{t('columnFilter.noneOf')}</option>
      </Select>
      {picker}
      {info.kind === 'person' && <Toggle checked={draft.me} onChange={(me) => patch({ me })} label={t('columnFilter.me')} />}
      {offersSubterms(info) && <Toggle checked={draft.sub} onChange={(sub) => patch({ sub })} label={t('columnFilter.subterms')} />}
      {info.kind === 'term' && !offersSubterms(info) && <p className="text-xs text-muted">{t('columnFilter.subtermsAlways')}</p>}
      {canPickEmpty(info) && <Toggle checked={draft.empty} onChange={(empty) => patch({ empty })} label={t('columnFilter.empty')} />}
    </div>
  );
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (checked: boolean) => void; label: string }) {
  return (
    <label className="flex items-center gap-2 text-[13px]">
      <Checkbox checked={checked} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  );
}

/** A list of values with checkboxes, searched as people type when it is long. */
function CheckList({
  label,
  options,
  selected,
  onToggle,
  searchable = options.length > 8,
  footer,
}: {
  label: string;
  options: { value: FilterValue; label: string; hint?: string }[];
  selected: FilterValue[];
  onToggle: (value: FilterValue, on: boolean) => void;
  searchable?: boolean;
  footer?: string;
}) {
  const { t } = useTranslation(['lists', 'common']);
  const [text, setText] = useState('');
  const shown = searchable && text.trim() ? options.filter((option) => option.label.toLowerCase().includes(text.trim().toLowerCase())) : options;
  return (
    <div className="grid gap-1">
      {searchable && <Input aria-label={t('columnFilter.findValue')} placeholder={t('common:search.placeholder')} value={text} onChange={(event) => setText(event.target.value)} />}
      <ul aria-label={label} className="max-h-56 overflow-y-auto rounded-md border p-1">
        {shown.map((option) => (
          <li key={String(option.value)}>
            <label className="flex items-center gap-2 rounded px-2 py-1 text-[13px] hover:bg-surface-muted">
              <Checkbox checked={selected.includes(option.value)} onChange={(event) => onToggle(option.value, event.target.checked)} />
              <span className="flex-1 truncate">{option.label}</span>
              {option.hint && <span className="text-xs text-muted">{option.hint}</span>}
            </label>
          </li>
        ))}
        {shown.length === 0 && <li className="px-2 py-2 text-xs text-muted">{t('common:search.noMatches')}</li>}
      </ul>
      {footer && <p className="text-xs text-muted">{footer}</p>}
    </div>
  );
}

/** How many distinct values a text column's list offers at once. */
const valuesShown = 100;

/**
 * The distinct values of a text column among the items the page shows (with the other column filters), with how many
 * items hold each, searched on the server as people type.
 */
function TextValues({ filtering, column, selected, onToggle }: { filtering: ColumnFiltering; column: string; selected: string[]; onToggle: (value: FilterValue, on: boolean) => void }) {
  const { t } = useTranslation('lists');
  const format = useFormat();
  const [text, setText] = useState('');
  const search = useDebounced(text.trim());
  const { sort: _sort, ...base } = filtering.valuesQuery(column);
  const body = { query: { ...base, group_by: column, filter: andFilter(base.filter, search ? { field: column, op: 'contains', value: search } : undefined) } };
  const groups = useQuery({
    queryKey: ['resources', filtering.collectionId, 'filter-values', body],
    queryFn: async () =>
      (await api<Page<QueryGroup>>(`/v1/resources/${filtering.collectionId}/query/groups`, { method: 'POST', query: { $top: valuesShown }, body })).data,
    placeholderData: keepPreviousData,
  });
  const found = (groups.data?.value ?? []).filter((group): group is QueryGroup & { value: string } => typeof group.value === 'string' && group.value !== '');
  // Values chosen earlier stay listed, even when they no longer match the search.
  const options = [
    ...selected.filter((value) => !found.some((group) => group.value === value)).map((value) => ({ value, label: value })),
    ...found.map((group) => ({ value: group.value, label: group.value, hint: format.number(group.count) })),
  ];
  return (
    <div className="grid gap-1">
      <Input aria-label={t('columnFilter.findValue')} placeholder={t('columnFilter.findValuePlaceholder')} value={text} onChange={(event) => setText(event.target.value)} />
      {groups.isPending ? (
        <Spinner />
      ) : groups.isError ? (
        <Alert>{t('columnFilter.valuesFailed')}</Alert>
      ) : (
        <CheckList
          label={t('columnFilter.values')}
          options={options}
          selected={selected}
          onToggle={onToggle}
          searchable={false}
          footer={groups.data?.['@odata.nextLink'] ? t('columnFilter.moreValues', { number: format.number(valuesShown) }) : undefined}
        />
      )}
    </div>
  );
}

/** Keywords picked from the shared keywords, by name as people type. */
function KeywordValues({ id, selected, onChange }: { id: string; selected: string[]; onChange: (values: string[]) => void }) {
  const [text, setText] = useState('');
  const search = useDebounced(text.trim());
  const found = useQuery({ queryKey: ['keywords', 'suggest', search], staleTime: 30_000, queryFn: () => keywords(search) });
  const [picked, setPicked] = useState<Map<string, string>>(new Map());
  const stored = useTermNames([...selected].sort());
  return (
    <Combobox
      id={id}
      aria-labelledby={`${id}-label`}
      multiple
      loading={found.isFetching}
      selected={selected.map((value) => ({ value, label: picked.get(value) ?? stored.get(value) ?? value }))}
      options={(found.data ?? []).map((term) => ({ value: term.id, label: term.name }))}
      onSearch={setText}
      onChange={(chosen) => {
        setPicked((current) => new Map([...current, ...chosen.map((option) => [option.value, option.label] as const)]));
        onChange(chosen.map((option) => option.value));
      }}
    />
  );
}

/** A sentence for each filter in force, naming people, terms and items, in the person's formats and language. */
export function useFilterSummary(active: [string, ColumnFilter][], fields: FieldDefinition[]) {
  const { t, i18n } = useTranslation(['lists', 'common']);
  const format = useFormat();
  const people = usePeopleNames();
  const idsOf = (kinds: string[]) =>
    [...new Set(active.flatMap(([column, filter]) => (filter.op === 'values' && kinds.includes(filterability(column, fields).kind ?? '') ? filter.values.filter((value): value is string => typeof value === 'string') : [])))].sort();
  const terms = useTermNames(idsOf(['term', 'keywords']));
  const items = useResourceNames(idsOf(['lookup']));
  const list = (entries: string[]) => {
    try {
      return new Intl.ListFormat(i18n.language, { type: 'disjunction' }).format(entries);
    } catch {
      return entries.join(', ');
    }
  };
  return (column: string, filter: ColumnFilter): string => {
    const info = filterability(column, fields);
    const label = columnLabel(column, fields);
    const shown = (text: string) => (info.kind === 'number' ? format.number(text) : info.kind === 'date' || info.kind === 'datetime' ? format.date(text) : text);
    switch (filter.op) {
      case 'contains':
        return t('columnFilter.summary.contains', { column: label, text: filter.text });
      case 'equals':
        return t('columnFilter.summary.equals', { column: label, value: shown(filter.value) });
      case 'range':
        return filter.from && filter.to
          ? t('columnFilter.summary.between', { column: label, from: shown(filter.from), to: shown(filter.to) })
          : filter.from
            ? t('columnFilter.summary.from', { column: label, from: shown(filter.from) })
            : t('columnFilter.summary.to', { column: label, to: shown(filter.to ?? '') });
      case 'empty':
        return t('columnFilter.summary.empty', { column: label });
      case 'present':
        return t('columnFilter.summary.present', { column: label });
      case 'values': {
        const name = (value: FilterValue) =>
          typeof value === 'boolean'
            ? t(value ? 'common:answer.yes' : 'common:answer.no')
            : ((info.kind === 'person' ? people.get(value) : info.kind === 'term' || info.kind === 'keywords' ? terms.get(value) : info.kind === 'lookup' ? items.get(value) : undefined) ?? value);
        const values = list([...filter.values.map(name), ...(filter.me ? [t('columnFilter.me')] : []), ...(filter.empty ? [t('columnFilter.empty')] : [])]);
        return filter.exclude
          ? t('columnFilter.summary.noneOf', { column: label, values })
          : filter.sub
            ? t('columnFilter.summary.under', { column: label, values })
            : t('columnFilter.summary.anyOf', { column: label, values });
      }
    }
  };
}
