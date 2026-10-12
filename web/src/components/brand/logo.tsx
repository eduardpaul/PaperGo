import { cn } from '@/lib/utils';

export function Logo({ className, withName = true }: { className?: string; withName?: boolean }) {
  return (
    <span className={cn('inline-flex items-center gap-2 font-semibold tracking-tight', className)}>
      <svg viewBox="0 0 32 32" className="size-7 shrink-0" aria-hidden>
        <rect width="32" height="32" rx="7" className="fill-accent" />
        <path d="M10 7h8l5 5v13a1 1 0 0 1-1 1H10a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1z" fill="#fff" />
        <path d="M18 7v5h5" fill="#c7d2fe" />
        <path d="M12 17h8M12 21h6" className="stroke-accent" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
      {withName && <span>PaperGo</span>}
    </span>
  );
}
