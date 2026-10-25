import { ContextMenu as Primitive } from 'radix-ui';
import type { ComponentProps } from 'react';
import { cn } from '@/lib/utils';

export const ContextMenu = Primitive.Root;
export const ContextMenuTrigger = Primitive.Trigger;
export const ContextMenuSub = Primitive.Sub;
const contentClass = 'z-50 min-w-48 overflow-hidden rounded-lg border bg-surface p-1 shadow-lg';
const itemClass = 'flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-[13px] outline-none select-none data-[disabled]:opacity-50 data-[highlighted]:bg-surface-muted';
export function ContextMenuContent(props: ComponentProps<typeof Primitive.Content>) {
  return <Primitive.Portal><Primitive.Content {...props} className={cn(contentClass, props.className)} /></Primitive.Portal>;
}
export function ContextMenuItem({ tone, ...props }: ComponentProps<typeof Primitive.Item> & { tone?: 'danger' }) {
  return <Primitive.Item {...props} className={cn(itemClass, tone === 'danger' && 'text-danger', props.className)} />;
}
export function ContextMenuSubTrigger(props: ComponentProps<typeof Primitive.SubTrigger>) {
  return <Primitive.SubTrigger {...props} className={cn(itemClass, props.className)} />;
}
export function ContextMenuSubContent(props: ComponentProps<typeof Primitive.SubContent>) {
  return <Primitive.Portal><Primitive.SubContent {...props} className={cn(contentClass, props.className)} /></Primitive.Portal>;
}
export function ContextMenuSeparator() { return <Primitive.Separator className="-mx-1 my-1 h-px bg-border" />; }
/** The key that does the same from the list, shown at the end of an item. */
export function ContextMenuShortcut({ children }: { children: string }) {
  return <kbd aria-hidden className="ml-auto pl-4 font-sans text-xs text-muted">{children}</kbd>;
}
