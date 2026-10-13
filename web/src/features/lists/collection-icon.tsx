import { BookUser, FileText, Library, List } from 'lucide-react';
import type { Resource } from '@/api/types';

/** The icon of a list or library: its template when it has a known one, otherwise its kind. */
export function CollectionIcon({
  collection,
  className,
}: {
  collection: Pick<Resource, 'kind'> & { template_keys?: string[] | null; key?: string };
  className?: string;
}) {
  const template = collection.key ?? collection.template_keys?.[0];
  const Icon =
    template === 'contacts' ? BookUser : template === 'documents' ? FileText : collection.kind === 'library' ? Library : List;
  return <Icon aria-hidden className={className} />;
}
