import { useQuery } from '@tanstack/react-query';
import { X } from 'lucide-react';
import { useId, useState, type KeyboardEvent } from 'react';
import { keywords } from '@/api/queries';
import { useTermNames } from '@/features/fields/names';
import { cn } from '@/lib/utils';

export const maxKeywords = 50;
export const maxKeywordLength = 255;

/** Shared folksonomy chips. New labels are resolved into keyword terms on save. */
export function KeywordInput({ id, value, onChange, disabled, limit = maxKeywords }: {
  id?: string;
  value: string[];
  onChange: (keywords: string[]) => void;
  disabled?: boolean;
  limit?: number;
}) {
  const listId = useId();
  const [text, setText] = useState('');
  const [focused, setFocused] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [picked, setPicked] = useState<Map<string, string>>(new Map());
  const stored = useTermNames([...new Set(value.filter(id => !picked.has(id)))].sort());
  const nameOf = (id: string) => picked.get(id) ?? stored.get(id) ?? id;
  const prefix = text.trim();
  const suggestions = useQuery({ queryKey: ['keywords', 'suggest', prefix], enabled: focused && !disabled, staleTime: 30_000, queryFn: () => keywords(prefix) });
  const options = (suggestions.data ?? []).filter(entry => !value.includes(entry.id));
  const open = focused && options.length > 0;
  const full = value.length >= limit;

  function add(raw: string | string[]) {
    setText(''); setHighlight(-1);
    const next = [...value];
    const names = new Map(picked);
    for (const entry of Array.isArray(raw) ? raw : [raw]) {
      const clean = entry.trim().slice(0, maxKeywordLength);
      if (!clean || next.length >= limit) continue;
      const known = options.find(option => option.id === clean || option.name.toLocaleLowerCase() === clean.toLocaleLowerCase());
      const key = known?.id ?? clean;
      const label = known?.name ?? clean;
      if (next.includes(key) || next.some(id => (names.get(id) ?? nameOf(id)).toLocaleLowerCase() === label.toLocaleLowerCase())) continue;
      names.set(key, label); next.push(key);
    }
    if (next.length === value.length) return;
    setPicked(names); onChange(next);
  }
  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown' && options.length) { event.preventDefault(); setHighlight(current => (current + 1) % options.length); }
    else if (event.key === 'ArrowUp' && options.length) { event.preventDefault(); setHighlight(current => current <= 0 ? options.length - 1 : current - 1); }
    else if (event.key === 'Enter' || event.key === ',' || (event.key === 'Tab' && prefix)) {
      const chosen = highlight >= 0 ? options[highlight]?.id : prefix;
      if (chosen) { event.preventDefault(); add(chosen); }
    } else if (event.key === 'Backspace' && !text && value.length) onChange(value.slice(0, -1));
    else if (event.key === 'Escape' && (text || highlight >= 0)) { event.preventDefault(); setText(''); setHighlight(-1); }
  }
  return <div className="relative">
    <div className={cn('flex min-h-9 w-full flex-wrap items-center gap-1 rounded-md border border-input bg-surface px-2 py-1 text-sm shadow-xs', 'focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/25', disabled && 'opacity-50')}>
      {value.map(keyword => <span key={keyword} className="inline-flex items-center gap-1 rounded bg-surface-muted px-1.5 py-0.5 text-xs">
        {nameOf(keyword)}
        {!disabled && <button type="button" aria-label={`Remove keyword ${nameOf(keyword)}`} className="text-muted hover:text-foreground" onClick={() => onChange(value.filter(entry => entry !== keyword))}><X className="size-3" /></button>}
      </span>)}
      <input id={id} role="combobox" aria-expanded={open} aria-controls={listId} aria-autocomplete="list" aria-activedescendant={highlight >= 0 ? `${listId}-${highlight}` : undefined} disabled={disabled || full} value={text} maxLength={maxKeywordLength} placeholder={full ? `At most ${limit} keywords` : value.length ? '' : 'Add keywords…'} className="min-w-24 flex-1 bg-transparent py-0.5 outline-none placeholder:text-muted"
        onChange={event => { const next = event.target.value; if (next.includes(',')) { const parts = next.split(','); add(parts.slice(0, -1)); setText(parts.at(-1) ?? ''); } else { setText(next); setHighlight(-1); } }} onKeyDown={onKeyDown} onFocus={() => setFocused(true)} onBlur={() => { setFocused(false); if (prefix) add(prefix); }} />
    </div>
    {open && <ul id={listId} role="listbox" className="absolute z-50 mt-1 max-h-56 w-full overflow-auto rounded-md border bg-surface p-1 shadow-lg">
      {options.map((keyword, index) => <li key={keyword.id} id={`${listId}-${index}`} role="option" aria-selected={index === highlight} className={cn('cursor-default rounded px-2 py-1.5 text-[13px]', index === highlight ? 'bg-surface-muted' : 'hover:bg-surface-muted')} onMouseDown={event => event.preventDefault()} onClick={() => add(keyword.id)}>{keyword.name}</li>)}
    </ul>}
  </div>;
}
