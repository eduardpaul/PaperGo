import { X } from 'lucide-react';
import { Dialog as Primitive } from 'radix-ui';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export const Sheet = Primitive.Root;
export const SheetTitle = Primitive.Title;
export const SheetDescription = Primitive.Description;

/** A panel on the right (full screen on phones), for item details next to the list they belong to. */
export function SheetContent({ className, children, ...props }: React.ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Overlay className="fixed inset-0 z-40 bg-black/25 md:bg-black/10" />
      <Primitive.Content
        className={cn(
          'fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l bg-surface shadow-2xl outline-none md:w-[min(640px,92vw)]',
          className,
        )}
        {...props}
      >
        {children}
      </Primitive.Content>
    </Primitive.Portal>
  );
}

export function SheetClose({ className }: { className?: string }) {
  return (
    <Primitive.Close className={cn('rounded-md p-1.5 text-muted hover:bg-surface-muted', className)} aria-label="Close">
      <X className="size-4" />
    </Primitive.Close>
  );
}
