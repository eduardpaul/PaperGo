import type * as React from 'react';
import { cn } from '@/lib/utils';

export function Input({ className, ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      className={cn(
        'h-9 w-full min-w-0 rounded-md border border-input bg-surface px-3 text-sm shadow-xs transition-colors outline-none placeholder:text-muted',
        'focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 disabled:opacity-50',
        'aria-invalid:border-danger aria-invalid:ring-danger/20',
        className,
      )}
      {...props}
    />
  );
}

export function Label({ className, ...props }: React.ComponentProps<'label'>) {
  return <label className={cn('text-[13px] leading-none font-medium', className)} {...props} />;
}

export function Textarea({ className, ...props }: React.ComponentProps<'textarea'>) {
  return (
    <textarea
      className={cn(
        'min-h-20 w-full rounded-md border border-input bg-surface px-3 py-2 text-sm shadow-xs outline-none placeholder:text-muted',
        'focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25',
        className,
      )}
      {...props}
    />
  );
}
