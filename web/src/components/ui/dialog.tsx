import { X } from 'lucide-react';
import { Dialog as Primitive } from 'radix-ui';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export const Dialog = Primitive.Root;
export const DialogTrigger = Primitive.Trigger;
export const DialogClose = Primitive.Close;

export function DialogContent({
  className,
  children,
  hideClose,
  ...props
}: React.ComponentProps<typeof Primitive.Content> & { hideClose?: boolean }) {
  return (
    <Primitive.Portal>
      <Primitive.Overlay className="fixed inset-0 z-50 bg-black/40 backdrop-blur-[1px]" />
      <Primitive.Content
        className={cn(
          'fixed top-[12vh] left-1/2 z-50 w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 rounded-xl border bg-surface shadow-2xl outline-none',
          className,
        )}
        {...props}
      >
        {children}
        {!hideClose && (
          <Primitive.Close
            className="absolute top-3 right-3 rounded-md p-1 text-muted hover:bg-surface-muted"
            aria-label="Close"
          >
            <X className="size-4" />
          </Primitive.Close>
        )}
      </Primitive.Content>
    </Primitive.Portal>
  );
}

export function DialogHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex flex-col gap-1 px-5 pt-5 pb-3', className)} {...props} />;
}

export function DialogTitle({ className, ...props }: React.ComponentProps<typeof Primitive.Title>) {
  return <Primitive.Title className={cn('text-base font-semibold', className)} {...props} />;
}

export function DialogDescription({ className, ...props }: React.ComponentProps<typeof Primitive.Description>) {
  return <Primitive.Description className={cn('text-[13px] text-muted', className)} {...props} />;
}

export function DialogFooter({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex justify-end gap-2 border-t px-5 py-3', className)} {...props} />;
}
