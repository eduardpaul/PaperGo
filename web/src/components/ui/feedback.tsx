import { LoaderCircle } from 'lucide-react';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export function Skeleton({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('animate-pulse rounded-md bg-surface-muted', className)} {...props} />;
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle aria-hidden className={cn('size-4 animate-spin text-muted', className)} />;
}

export function Kbd({ className, ...props }: React.ComponentProps<'kbd'>) {
  return (
    <kbd
      className={cn(
        'rounded border bg-surface px-1.5 font-sans text-[11px] leading-5 font-medium text-muted',
        className,
      )}
      {...props}
    />
  );
}

export function EmptyState({
  icon: Icon,
  title,
  children,
  className,
}: {
  icon?: React.ComponentType<{ className?: string }>;
  title: string;
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-2 px-6 py-10 text-center', className)}>
      {Icon && <Icon className="size-8 text-muted/70" />}
      <p className="font-medium">{title}</p>
      {children && <div className="max-w-sm text-[13px] text-muted">{children}</div>}
    </div>
  );
}

export function Alert({
  tone = 'danger',
  className,
  ...props
}: React.ComponentProps<'div'> & { tone?: 'danger' | 'warning' | 'success' }) {
  const tones = {
    danger: 'bg-danger-soft text-danger border-danger/30',
    warning: 'bg-warning-soft text-foreground border-warning/40',
    success: 'bg-success-soft text-success border-success/30',
  };
  return (
    <div role="alert" className={cn('rounded-md border px-3 py-2 text-[13px]', tones[tone], className)} {...props} />
  );
}
