import { Tabs as Primitive } from 'radix-ui';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export const Tabs = Primitive.Root;
export const TabsContent = Primitive.Content;

export function TabsList({ className, ...props }: React.ComponentProps<typeof Primitive.List>) {
  return <Primitive.List className={cn('flex gap-1 border-b px-4', className)} {...props} />;
}

export function TabsTrigger({ className, ...props }: React.ComponentProps<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={cn(
        '-mb-px border-b-2 border-transparent px-2.5 py-2 text-[13px] font-medium whitespace-nowrap text-muted hover:text-foreground',
        'data-[state=active]:border-accent data-[state=active]:text-foreground',
        className,
      )}
      {...props}
    />
  );
}
