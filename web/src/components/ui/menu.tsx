import { DropdownMenu as Primitive } from 'radix-ui';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export const DropdownMenu = Primitive.Root;
export const DropdownMenuTrigger = Primitive.Trigger;
export const DropdownMenuGroup = Primitive.Group;

export function DropdownMenuContent({
  className,
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        sideOffset={sideOffset}
        className={cn('z-50 min-w-48 overflow-hidden rounded-lg border bg-surface p-1 shadow-lg', className)}
        {...props}
      />
    </Primitive.Portal>
  );
}

export function DropdownMenuItem({
  className,
  tone,
  ...props
}: React.ComponentProps<typeof Primitive.Item> & { tone?: 'danger' }) {
  return (
    <Primitive.Item
      className={cn(
        'flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-[13px] outline-none select-none',
        'data-[disabled]:opacity-50 data-[highlighted]:bg-surface-muted [&_svg]:size-4 [&_svg]:text-muted',
        tone === 'danger' && 'text-danger [&_svg]:text-danger',
        className,
      )}
      {...props}
    />
  );
}

export function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof Primitive.Label>) {
  return <Primitive.Label className={cn('px-2 py-1.5 text-xs text-muted', className)} {...props} />;
}

export function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof Primitive.Separator>) {
  return <Primitive.Separator className={cn('-mx-1 my-1 h-px bg-border', className)} {...props} />;
}

export const DropdownMenuRadioGroup = Primitive.RadioGroup;

export function DropdownMenuRadioItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof Primitive.RadioItem>) {
  return (
    <Primitive.RadioItem
      className={cn(
        'flex cursor-default items-center gap-2 rounded-md py-1.5 pr-2 pl-7 text-[13px] outline-none select-none data-[highlighted]:bg-surface-muted',
        'relative',
        className,
      )}
      {...props}
    >
      <Primitive.ItemIndicator className="absolute left-2.5 size-1.5 rounded-full bg-current" />
      {children}
    </Primitive.RadioItem>
  );
}
