import { Popover as Primitive, Tooltip as TooltipPrimitive } from 'radix-ui';
import type * as React from 'react';
import { cn } from '@/lib/utils';

export const Popover = Primitive.Root;
export const PopoverTrigger = Primitive.Trigger;
export const PopoverClose = Primitive.Close;

export function PopoverContent({
  className,
  sideOffset = 6,
  align = 'end',
  ...props
}: React.ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        sideOffset={sideOffset}
        align={align}
        className={cn('z-50 rounded-lg border bg-surface shadow-lg outline-none', className)}
        {...props}
      />
    </Primitive.Portal>
  );
}

export const TooltipProvider = TooltipPrimitive.Provider;

/** A short label on hover and focus, for icon buttons. */
export function Tooltip({
  label,
  children,
  side = 'bottom',
}: {
  label: React.ReactNode;
  children: React.ReactNode;
  side?: 'top' | 'bottom' | 'left' | 'right';
}) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={6}
          className="z-50 rounded-md bg-foreground px-2 py-1 text-xs text-background shadow-md"
        >
          {label}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}
