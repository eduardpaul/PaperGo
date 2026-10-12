import { ChevronDown } from 'lucide-react';
import type * as React from 'react';
import { cn } from '@/lib/utils';

/** A native select (keyboard, mobile pickers and screen readers work as users expect), styled like inputs. */
export function Select({ className, children, ...props }: React.ComponentProps<'select'>) {
  return (
    <div className={cn('relative', className)}>
      <select
        className={cn(
          'h-9 w-full appearance-none rounded-md border border-input bg-surface pr-8 pl-3 text-sm shadow-xs outline-none',
          'focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 disabled:opacity-50',
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown className="pointer-events-none absolute top-2.5 right-2.5 size-4 text-muted" />
    </div>
  );
}

export function Checkbox({ className, ...props }: Omit<React.ComponentProps<'input'>, 'type'>) {
  return (
    <input
      type="checkbox"
      className={cn('size-4 shrink-0 cursor-pointer rounded border align-middle accent-accent', className)}
      {...props}
    />
  );
}
