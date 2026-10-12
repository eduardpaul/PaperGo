import { useQuery } from '@tanstack/react-query';
import { X } from 'lucide-react';
import { useId, useState, type KeyboardEvent } from 'react';
import { api, listOf } from '@/api/http';
import type { Page } from '@/api/types';
import { cn } from '@/lib/utils';

export const maxTags = 50;
export const maxTagLength = 64;

/**
 * Tags as chips. Enter, Tab or a comma adds the typed tag; Backspace in an empty box removes the last one. Suggestions
 * come from the tags used in the workspace.
 */
export function TagInput({
  id,
  value,
  onChange,
  workspaceId,
  disabled,
}: {
  id?: string;
  value: string[];
  onChange: (tags: string[]) => void;
  workspaceId: string;
  disabled?: boolean;
}) {
  const listId = useId();
  const [text, setText] = useState('');
  const [focused, setFocused] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const prefix = text.trim();
  const suggestions = useQuery({
    queryKey: ['tags', workspaceId, 'suggest', prefix],
    enabled: focused && !disabled,
    staleTime: 30_000,
    queryFn: async () =>
      listOf(
        (
          await api<Page<{ tag: string; count: number }>>(`/v1/workspaces/${workspaceId}/tags`, {
            query: { prefix: prefix || undefined, $top: 10 },
          })
        ).data,
      ),
  });
  const options = (suggestions.data ?? []).map((entry) => entry.tag).filter((tag) => !value.includes(tag));
  const open = focused && options.length > 0;
  const full = value.length >= maxTags;

  function add(tag: string) {
    const clean = tag.trim().slice(0, maxTagLength);
    setText('');
    setHighlight(-1);
    if (!clean || value.includes(clean) || full) return;
    onChange([...value, clean]);
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown' && options.length) {
      event.preventDefault();
      setHighlight((current) => (current + 1) % options.length);
    } else if (event.key === 'ArrowUp' && options.length) {
      event.preventDefault();
      setHighlight((current) => (current <= 0 ? options.length - 1 : current - 1));
    } else if (event.key === 'Enter' || event.key === ',' || (event.key === 'Tab' && prefix)) {
      const chosen = highlight >= 0 ? options[highlight] : prefix;
      if (!chosen) return;
      event.preventDefault();
      add(chosen);
    } else if (event.key === 'Backspace' && !text && value.length) {
      onChange(value.slice(0, -1));
    } else if (event.key === 'Escape' && (text || highlight >= 0)) {
      event.preventDefault();
      setText('');
      setHighlight(-1);
    }
  }

  return (
    <div className="relative">
      <div
        className={cn(
          'flex min-h-9 w-full flex-wrap items-center gap-1 rounded-md border border-input bg-surface px-2 py-1 text-sm shadow-xs',
          'focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/25',
          disabled && 'opacity-50',
        )}
      >
        {value.map((tag) => (
          <span key={tag} className="inline-flex items-center gap-1 rounded bg-surface-muted px-1.5 py-0.5 text-xs">
            {tag}
            {!disabled && (
              <button
                type="button"
                aria-label={`Remove tag ${tag}`}
                className="text-muted hover:text-foreground"
                onClick={() => onChange(value.filter((entry) => entry !== tag))}
              >
                <X className="size-3" />
              </button>
            )}
          </span>
        ))}
        <input
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={highlight >= 0 ? `${listId}-${highlight}` : undefined}
          disabled={disabled || full}
          value={text}
          maxLength={maxTagLength}
          placeholder={full ? `At most ${maxTags} tags` : value.length ? '' : 'Add tags…'}
          className="min-w-24 flex-1 bg-transparent py-0.5 outline-none placeholder:text-muted"
          onChange={(event) => {
            const next = event.target.value;
            if (next.includes(',')) {
              next.split(',').slice(0, -1).forEach(add);
              setText(next.split(',').at(-1) ?? '');
            } else {
              setText(next);
              setHighlight(-1);
            }
          }}
          onKeyDown={onKeyDown}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            if (prefix) add(prefix);
          }}
        />
      </div>
      {open && (
        <ul
          id={listId}
          role="listbox"
          className="absolute z-50 mt-1 max-h-56 w-full overflow-auto rounded-md border bg-surface p-1 shadow-lg"
        >
          {options.map((tag, index) => (
            <li
              key={tag}
              id={`${listId}-${index}`}
              role="option"
              aria-selected={index === highlight}
              className={cn(
                'cursor-default rounded px-2 py-1.5 text-[13px]',
                index === highlight ? 'bg-surface-muted' : 'hover:bg-surface-muted',
              )}
              // Keep focus in the input so the click adds the tag before blur.
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => add(tag)}
            >
              {tag}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
