import type * as React from 'react';
import { cn } from '@/lib/utils';

/** The content area of a screen: a readable width with the same gutters everywhere. */
export function Page({ className, wide, ...props }: React.ComponentProps<'div'> & { wide?: boolean }) {
  return (
    <div
      className={cn('mx-auto w-full px-4 py-6 sm:px-6 lg:py-8', wide ? 'max-w-none' : 'max-w-6xl', className)}
      {...props}
    />
  );
}

export function PageHeader({
  title,
  description,
  icon: Icon,
  actions,
  children,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ComponentType<{ className?: string }>;
  actions?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <header className="mb-6 flex flex-wrap items-start gap-x-4 gap-y-3">
      <div className="flex min-w-0 flex-1 items-start gap-3">
        {Icon && (
          <span className="mt-0.5 rounded-lg bg-accent-soft p-2 text-accent">
            <Icon className="size-5" />
          </span>
        )}
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
          {description && <p className="mt-0.5 text-[13px] text-muted">{description}</p>}
        </div>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
      {children}
    </header>
  );
}
