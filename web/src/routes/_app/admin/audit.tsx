import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { useApiPages } from '@/api/pages';
import { stringifyJSON } from '@/api/json';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Field, QueryFeedback, useHasScope } from '@/features/admin/common';
import { SettingsSection } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';

const filterKeys = ['actor', 'entity_type', 'entity_id', 'workspace_id', 'from', 'to'] as const;
type Filters = Partial<Record<(typeof filterKeys)[number], string>>;
interface AuditEvent {
  id: string;
  created_at: string;
  subject: string;
  action: string;
  entity_type: string;
  entity_id: string;
  properties?: string[];
  details?: Record<string, unknown>;
  /** The access token the action was taken with, such as an AI assistant's. */
  credential?: { id: string; name?: string };
}
export const Route = createFileRoute('/_app/admin/audit')({
  validateSearch: (search: Record<string, unknown>): Filters =>
    Object.fromEntries(filterKeys.map((key) => [key, typeof search[key] === 'string' ? search[key] : undefined])),
  component: AuditPage,
});

function AuditPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const format = useFormat();
  const canRead = useHasScope('audit.read');
  const entries = useApiPages<AuditEvent>(['auditLog'], '/v1/audit', { enabled: canRead, query: search });
  if (!canRead) return <Alert>You need audit.read to view the audit log.</Alert>;
  return (
    <SettingsSection title="Audit log" description="Who changed what and when, newest first.">
      <AuditFilters
        key={JSON.stringify(search)}
        current={search}
        onApply={(filters) => void navigate({ search: filters })}
      />
      <QueryFeedback query={entries} />
      <div className="overflow-x-auto">
        <table className="w-full text-left text-[13px]">
          <thead className="bg-surface-muted text-muted">
            <tr>
              {['When', 'Who', 'What', 'Changed'].map((column) => (
                <th key={column} className="p-2">
                  {column}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {entries.items.map((entry) => (
              <tr key={entry.id}>
                <td className="p-2 whitespace-nowrap">{format.dateTime(entry.created_at)}</td>
                <td className="p-2">
                  {entry.subject || 'System'}
                  {entry.credential && (
                    <p className="mt-1 text-xs text-muted">via {entry.credential.name ?? 'a removed access token'}</p>
                  )}
                </td>
                <td className="p-2">
                  <Badge>{entry.action}</Badge>
                  <p className="mt-1 text-xs text-muted">
                    {entry.entity_type} · {entry.entity_id}
                  </p>
                </td>
                <td className="p-2">
                  {entry.properties?.join(', ')}
                  {entry.details && (
                    <details>
                      <summary className="cursor-pointer text-xs text-accent">Details</summary>
                      <pre className="mt-2 max-w-md overflow-auto whitespace-pre-wrap text-xs">
                        {stringifyJSON(entry.details)}
                      </pre>
                    </details>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!entries.isPending && !entries.error && entries.items.length === 0 && (
        <EmptyState title="Nothing recorded for these filters" />
      )}
      {entries.hasNextPage && (
        <Button className="mt-3" disabled={entries.isFetchingNextPage} onClick={() => void entries.fetchNextPage()}>
          Show more
        </Button>
      )}
    </SettingsSection>
  );
}
function AuditFilters({ current, onApply }: { current: Filters; onApply: (filters: Filters) => void }) {
  const [draft, setDraft] = useState(current);
  return (
    <form
      className="mb-4 flex flex-wrap items-end gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        const filters = { ...draft };
        for (const key of ['from', 'to'] as const)
          if (filters[key]) filters[key] = new Date(filters[key]!).toISOString();
        onApply(filters);
      }}
    >
      {filterKeys.map((key) => (
        <Field
          key={key}
          id={`audit-${key}`}
          label={
            {
              actor: 'Who (subject)',
              entity_type: 'Entity type',
              entity_id: 'Entity id',
              workspace_id: 'Workspace id',
              from: 'From (local time)',
              to: 'Until (local time)',
            }[key]
          }
        >
          <Input
            id={`audit-${key}`}
            type={key === 'from' || key === 'to' ? 'datetime-local' : 'text'}
            value={key === 'from' || key === 'to' ? localDate(draft[key]) : (draft[key] ?? '')}
            onChange={(event) => setDraft((state) => ({ ...state, [key]: event.target.value || undefined }))}
            className="max-w-48"
          />
        </Field>
      ))}
      <Button type="submit" variant="primary">
        Filter
      </Button>
      <Button onClick={() => onApply({})}>Clear</Button>
    </form>
  );
}
function localDate(value?: string) {
  if (!value) return '';
  if (!value.endsWith('Z') && !/[+-]\d\d:\d\d$/.test(value)) return value;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (part: number) => String(part).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
