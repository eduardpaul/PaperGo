import { Link } from '@tanstack/react-router';
import type { Resource } from '@/api/types';
import { useTermNames } from '@/features/fields/names';
import { usePeopleNames } from '@/features/fields/people';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';
import { useSearchAvailable } from '@/lib/search';

/** Intrinsic properties are shown separately from configurable schema fields. */
export function SystemProperties({ item, file, keywords = [], contentType }: { item?: Resource; keywords?: string[]; contentType?: string; file?: { filename: string; content_type: string; size: number } }) {
  const { t } = useTranslation('lists');
  const format = useFormat();
  const people = usePeopleNames();
  const assigned = [...new Set([...(item?.keywords ?? []), ...keywords])];
  const terms = useTermNames(assigned);
  const searchAvailable = useSearchAvailable();
  const properties = [
    [t('system.name'), t('system.types.text'), item?.name],
    [t('system.archiveStatus'), t('system.types.status'), item?.archived ? t('system.archived') : t('system.active')],
    [t('columns.archived_at'), t('system.types.dateTime'), item?.archived_at ? format.dateTime(item.archived_at) : undefined],
    [t('columns.archived_by'), t('system.types.person'), item?.archived_by ? people.get(item.archived_by) ?? item.archived_by : undefined],
    [t('system.fileName'), t('system.types.text'), file?.filename],
    [t('system.fileSize'), t('system.types.bytes'), file ? format.fileSize(file.size) : undefined],
    [t('system.fileType'), t('system.types.mediaType'), file?.content_type],
    [t('system.contentType'), t('system.types.contentType'), contentType ?? item?.content_type_id],
    [t('columns.created_at'), t('system.types.dateTime'), item?.created_at ? format.dateTime(item.created_at) : undefined],
    [t('columns.created_by'), t('system.types.person'), item?.created_by ? people.get(item.created_by) ?? item.created_by : undefined],
    [t('columns.modified_at'), t('system.types.dateTime'), item?.updated_at ? format.dateTime(item.updated_at) : undefined],
    [t('columns.modified_by'), t('system.types.person'), item?.updated_by ? people.get(item.updated_by) ?? item.updated_by : undefined],
  ];
  return <section aria-label={t('system.title')} className="grid gap-2">
    <h3 className="font-medium">{t('system.title')}</h3>
    <dl className="grid gap-2 text-sm sm:grid-cols-2">
      {properties.map(([label, type, value]) => <div key={label}>
        <dt className="text-muted">{label} <span className="text-xs">· {type}</span></dt>
        {item && <dd className="break-words">{value ?? '—'}</dd>}
      </div>)}
      <div><dt className="text-muted">{t('columns.keywords')} <span className="text-xs">· {t('system.types.terms')}</span></dt>
        {item && <dd className="flex flex-wrap gap-1">{assigned.length ? assigned.map(keyword => searchAvailable ? <Link key={keyword} to="/search" search={{ q: undefined, workspace: undefined, collection: undefined, kind: undefined, keyword }} className="rounded border px-1.5 text-accent hover:underline">{terms.get(keyword) ?? keyword}</Link> : <span key={keyword} className="rounded border px-1.5">{terms.get(keyword) ?? keyword}</span>) : '—'}</dd>}
      </div>
    </dl>
  </section>;
}
