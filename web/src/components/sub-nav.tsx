import { createLink } from '@tanstack/react-router';
import type * as React from 'react';
import { cn } from '@/lib/utils';

/** The pages of an area (settings, workspace settings): a column on wide screens, a scrolling row on phones. */
export function SubNav({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <nav aria-label={label} className="-mx-4 overflow-x-auto px-4 md:mx-0 md:w-52 md:shrink-0 md:px-0">
      <ul className="flex gap-1 md:flex-col md:gap-0.5">{children}</ul>
    </nav>
  );
}

function SubNavAnchor({ className, ...props }: React.ComponentProps<'a'>) {
  return (
    <li>
      <a
        className={cn(
          'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] whitespace-nowrap text-foreground/80 hover:bg-surface-muted hover:text-foreground [&_svg]:size-4 [&_svg]:text-muted',
          'data-[status=active]:bg-accent-soft data-[status=active]:font-medium data-[status=active]:text-accent data-[status=active]:[&_svg]:text-accent',
          className,
        )}
        {...props}
      />
    </li>
  );
}

/** A typed router link for SubNav; the router marks the current page (data-status="active"). */
export const SubNavLink = createLink(SubNavAnchor);

/** The layout of an area with a SubNav: the navigation beside the page. */
export function SubNavLayout({ nav, children }: { nav: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-6 md:flex-row md:items-start">
      {nav}
      <div className="flex min-w-0 flex-1 flex-col gap-5">{children}</div>
    </div>
  );
}
