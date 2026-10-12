import { Check, Copy } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Alert, Spinner } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';
import { cn } from '@/lib/utils';

/** A titled block of a settings page, with actions at the bottom (e.g. Save). */
export function SettingsSection({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  const id = `section-${title.toLowerCase().replace(/\W+/g, '-')}`;
  return (
    <Card aria-labelledby={id}>
      <header className="px-5 pt-4 pb-3">
        <h2 id={id} className="text-sm font-semibold">
          {title}
        </h2>
        {description && <p className="mt-0.5 text-[13px] text-muted">{description}</p>}
      </header>
      {children && <div className={cn('px-5 pb-5', className)}>{children}</div>}
      {actions && (
        <footer className="flex items-center justify-end gap-2 rounded-b-lg border-t bg-surface-muted/40 px-5 py-3">
          {actions}
        </footer>
      )}
    </Card>
  );
}

/** A label, a control and an optional hint, in one row on wide screens. */
export function SettingRow({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5 py-2.5 sm:grid-cols-[12rem_1fr] sm:items-start sm:gap-4">
      <label htmlFor={id} className="pt-2 text-[13px] font-medium">
        {label}
      </label>
      <div className="flex min-w-0 flex-col gap-1">
        {children}
        {hint && <p className="text-xs text-muted">{hint}</p>}
      </div>
    </div>
  );
}

/** A secret or URL shown once, with a copy button. */
export function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      toast.success('Copied.');
    } catch {
      toast.error('Copying is not allowed here; select the text and copy it yourself.');
    }
  };
  return (
    <div className="flex gap-2">
      <Input
        aria-label={label}
        readOnly
        value={value}
        className="font-mono text-xs"
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button onClick={() => void copy()} aria-label={`Copy ${label.toLowerCase()}`}>
        {copied ? <Check /> : <Copy />} Copy
      </Button>
    </div>
  );
}

/** Asks before something that cannot be undone (revoking a token, removing a passkey). */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirm,
  busy,
  error,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  confirm: string;
  busy?: boolean;
  error?: unknown;
  onConfirm: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent role="alertdialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {!!error && <Alert className="mx-5 mb-3">{problemMessage(error)}</Alert>}
        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="danger" disabled={busy} onClick={onConfirm}>
            {busy && <Spinner />}
            {confirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
