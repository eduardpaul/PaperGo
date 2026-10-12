import { cn, initials } from '@/lib/utils';

const palette = [
  'bg-indigo-500',
  'bg-sky-500',
  'bg-emerald-500',
  'bg-amber-500',
  'bg-rose-500',
  'bg-violet-500',
  'bg-teal-500',
];

/** Initials on a color picked from the name, so the same person always looks the same. */
export function Avatar({ name, className }: { name: string | null | undefined; className?: string }) {
  let hash = 0;
  for (const char of name ?? '') hash = (hash * 31 + char.charCodeAt(0)) | 0;
  return (
    <span
      aria-hidden
      className={cn(
        'inline-flex size-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold text-white',
        palette[Math.abs(hash) % palette.length],
        className,
      )}
    >
      {initials(name)}
    </span>
  );
}
