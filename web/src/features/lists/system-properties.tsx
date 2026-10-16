import { Link } from '@tanstack/react-router';
import type { Resource } from '@/api/types';
import { useTermNames } from '@/features/fields/names';
import { usePeopleNames } from '@/features/fields/people';
import { useFormat } from '@/lib/preferences';

/** Intrinsic properties are shown separately from configurable schema fields. */
export function SystemProperties({ item, file, keywords = [], contentType }: { item?: Resource; keywords?: string[]; contentType?: string; file?: { filename: string; content_type: string; size: number } }) {
  const format = useFormat();
  const people = usePeopleNames();
  const terms = useTermNames(keywords);
  const properties = [
    ['Name', 'Text', item?.name],
    ['File name', 'Text', file?.filename],
    ['File size', 'Bytes', file ? format.fileSize(file.size) : undefined],
    ['File type', 'Media type', file?.content_type],
    ['Content type', 'Content type', contentType ?? item?.content_type_id],
    ['Created', 'Date and time', item?.created_at ? format.dateTime(item.created_at) : undefined],
    ['Created by', 'Person', item?.created_by ? people.get(item.created_by) ?? item.created_by : undefined],
    ['Modified', 'Date and time', item?.updated_at ? format.dateTime(item.updated_at) : undefined],
    ['Modified by', 'Person', item?.updated_by ? people.get(item.updated_by) ?? item.updated_by : undefined],
  ];
  return <section aria-label="System properties" className="grid gap-2">
    <h3 className="font-medium">System properties</h3>
    <dl className="grid gap-2 text-sm sm:grid-cols-2">
      {properties.map(([label, type, value]) => <div key={label}>
        <dt className="text-muted">{label} <span className="text-xs">· {type}</span></dt>
        {item && <dd className="break-words">{value ?? '—'}</dd>}
      </div>)}
      <div><dt className="text-muted">Tags / Keywords <span className="text-xs">· Terms</span></dt>
        {item && <dd className="flex flex-wrap gap-1">{(item.tags?.length || keywords.length) ? <>{item.tags?.map(tag => <Link key={tag} to="/search" search={{ q: undefined, workspace: undefined, collection: undefined, kind: undefined, tag }} className="rounded border px-1.5 text-accent hover:underline">{tag}</Link>)}{keywords.map(id => <span key={id} className="rounded border px-1.5">{terms.get(id) ?? id}</span>)}</> : '—'}</dd>}
      </div>
    </dl>
  </section>;
}
