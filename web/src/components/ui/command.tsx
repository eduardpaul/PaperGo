import { Command as Primitive } from 'cmdk';
import { Search } from 'lucide-react';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export function Command({ className, ...props }: React.ComponentProps<typeof Primitive>) {
  return <Primitive className={cn('flex flex-col overflow-hidden', className)} {...props} />;
}

export function CommandInput({ className, ...props }: React.ComponentProps<typeof Primitive.Input>) {
  return (
    <div className="flex items-center gap-2 border-b px-4">
      <Search className="size-4 shrink-0 text-muted" />
      <Primitive.Input
        className={cn('h-12 w-full bg-transparent text-[15px] outline-none placeholder:text-muted', className)}
        {...props}
      />
    </div>
  );
}

export function CommandList({ className, ...props }: React.ComponentProps<typeof Primitive.List>) {
  return <Primitive.List className={cn('max-h-[min(60vh,420px)] overflow-y-auto p-2', className)} {...props} />;
}

export function CommandEmpty(props: React.ComponentProps<typeof Primitive.Empty>) {
  return <Primitive.Empty className="py-8 text-center text-[13px] text-muted" {...props} />;
}

export function CommandGroup({ className, ...props }: React.ComponentProps<typeof Primitive.Group>) {
  return (
    <Primitive.Group
      className={cn(
        'py-1 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:text-muted',
        className,
      )}
      {...props}
    />
  );
}

export function CommandItem({ className, ...props }: React.ComponentProps<typeof Primitive.Item>) {
  return (
    <Primitive.Item
      className={cn(
        'flex cursor-default items-center gap-3 rounded-md px-2 py-2 text-[13px] select-none data-[selected=true]:bg-surface-muted [&_svg]:size-4 [&_svg]:text-muted',
        className,
      )}
      {...props}
    />
  );
}
