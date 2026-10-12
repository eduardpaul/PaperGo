import type * as React from 'react';
import { cn } from '@/lib/utils';

export function Card({ className, ...props }: React.ComponentProps<'section'>) {
  return <section className={cn('rounded-lg border bg-surface shadow-xs', className)} {...props} />;
}

export function CardHeader({ className, ...props }: React.ComponentProps<'header'>) {
  return <header className={cn('flex items-center gap-2 border-b px-4 py-3', className)} {...props} />;
}

export function CardTitle({ className, ...props }: React.ComponentProps<'h2'>) {
  return <h2 className={cn('text-sm font-semibold', className)} {...props} />;
}

export function CardContent({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('p-4', className)} {...props} />;
}
