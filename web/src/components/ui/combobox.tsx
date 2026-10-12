import { Command as CommandPrimitive } from 'cmdk';
import { Check, ChevronDown, Plus, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { cn } from '@/lib/utils';
import { Spinner } from './feedback';
import { Popover, PopoverContent, PopoverTrigger } from './popover';

export interface ComboboxOption {
  value: string;
  label: string;
  hint?: string;
  color?: string | null;
  icon?: ReactNode;
}

/**
 * Picks one or several values from options that may come from the server as the user types (people, items, terms).
 * `selected` are the options of the current values, so their labels show before the options load.
 */
export function Combobox({
  id,
  selected,
  options,
  multiple,
  loading,
  placeholder = 'Select…',
  onSearch,
  onChange,
  onCreate,
  disabled,
  'aria-invalid': invalid,
  'aria-labelledby': labelledBy,
}: {
  id?: string;
  selected: ComboboxOption[];
  options: ComboboxOption[];
  multiple?: boolean;
  loading?: boolean;
  placeholder?: string;
  /** Called with the text typed; without it, options are filtered locally. */
  onSearch?: (text: string) => void;
  onChange: (values: ComboboxOption[]) => void;
  /** Offers "Add …" for text that matches no option (open term sets, keywords). */
  onCreate?: (text: string) => void;
  disabled?: boolean;
  'aria-invalid'?: boolean;
  /** The id of the visible label (a <label for> cannot name a custom combobox). */
  'aria-labelledby'?: string;
}) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('');
  const chosen = new Set(selected.map((o) => o.value));
  const toggle = (option: ComboboxOption) => {
    if (multiple) {
      onChange(chosen.has(option.value) ? selected.filter((o) => o.value !== option.value) : [...selected, option]);
    } else {
      onChange(chosen.has(option.value) ? [] : [option]);
      setOpen(false);
    }
  };
  const exact = options.some((o) => o.label.toLowerCase() === text.trim().toLowerCase());

  return (
    <Popover
      open={open}
      onOpenChange={(value) => {
        setOpen(value);
        if (!value) setText('');
      }}
    >
      <PopoverTrigger asChild disabled={disabled}>
        <div
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-invalid={invalid}
          aria-labelledby={labelledBy}
          tabIndex={disabled ? -1 : 0}
          className={cn(
            'flex min-h-9 w-full cursor-pointer flex-wrap items-center gap-1 rounded-md border border-input bg-surface py-1 pr-8 pl-2 text-sm shadow-xs outline-none',
            'relative focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 aria-invalid:border-danger',
            disabled && 'pointer-events-none opacity-50',
          )}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
              e.preventDefault();
              setOpen(true);
            }
          }}
        >
          {selected.length === 0 && <span className="px-1 text-muted">{placeholder}</span>}
          {selected.map((option) =>
            multiple ? (
              <span
                key={option.value}
                className="inline-flex items-center gap-1 rounded bg-surface-muted px-1.5 py-0.5 text-xs"
              >
                {option.color && <span className="size-2 rounded-full" style={{ background: option.color }} />}
                {option.label}
                <button
                  type="button"
                  aria-label={`Remove ${option.label}`}
                  className="text-muted hover:text-foreground"
                  onClick={(e) => {
                    e.stopPropagation();
                    toggle(option);
                  }}
                >
                  <X className="size-3" />
                </button>
              </span>
            ) : (
              <span key={option.value} className="flex items-center gap-1.5 px-1">
                {option.icon}
                {option.color && <span className="size-2 rounded-full" style={{ background: option.color }} />}
                {option.label}
              </span>
            ),
          )}
          <ChevronDown className="absolute top-2.5 right-2.5 size-4 text-muted" />
        </div>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-64 p-0">
        <CommandPrimitive shouldFilter={!onSearch} loop>
          <div className="flex items-center border-b px-3">
            <CommandPrimitive.Input
              value={text}
              onValueChange={(value) => {
                setText(value);
                onSearch?.(value);
              }}
              placeholder="Search…"
              className="h-9 w-full bg-transparent text-sm outline-none placeholder:text-muted"
            />
            {loading && <Spinner />}
          </div>
          <CommandPrimitive.List className="max-h-64 overflow-y-auto p-1">
            <CommandPrimitive.Empty className="px-2 py-4 text-center text-xs text-muted">
              {loading ? 'Searching…' : 'No matches.'}
            </CommandPrimitive.Empty>
            {options.map((option) => (
              <CommandPrimitive.Item
                key={option.value}
                value={`${option.label} ${option.value}`}
                onSelect={() => toggle(option)}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1.5 text-[13px] data-[selected=true]:bg-surface-muted"
              >
                <Check className={cn('size-3.5', chosen.has(option.value) ? 'opacity-100' : 'opacity-0')} />
                {option.icon}
                {option.color && <span className="size-2 rounded-full" style={{ background: option.color }} />}
                <span className="flex-1 truncate">{option.label}</span>
                {option.hint && <span className="truncate text-xs text-muted">{option.hint}</span>}
              </CommandPrimitive.Item>
            ))}
            {onCreate && text.trim() && !exact && (
              <CommandPrimitive.Item
                value={`create ${text}`}
                onSelect={() => {
                  onCreate(text.trim());
                  setText('');
                }}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1.5 text-[13px] text-accent data-[selected=true]:bg-surface-muted"
              >
                <Plus className="size-3.5" /> Add “{text.trim()}”
              </CommandPrimitive.Item>
            )}
          </CommandPrimitive.List>
        </CommandPrimitive>
      </PopoverContent>
    </Popover>
  );
}
