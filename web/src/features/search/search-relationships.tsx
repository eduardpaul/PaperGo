import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { useApiPages } from '@/api/pages';
import type { Relationship } from '@/api/types';
import { Button } from '@/components/ui/button';
import { QueryFeedback } from '@/features/admin/common';
import { relationshipTypesQuery } from '@/features/admin/relationship-types';

/** Load authorized related resources only when a result's relationships are expanded. */
export function SearchRelationships({ itemId }: { itemId: string }) {
  const [open, setOpen] = useState(false);
  const links = useApiPages<Relationship>(['relationships', itemId, 'search'], `/v1/items/${itemId}/relationships`, { enabled: open, query: { direction: 'both', $top: 10 } });
  const types = useQuery({ ...relationshipTypesQuery, enabled: open });
  return <details className="px-4 pb-2 text-xs" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary className="cursor-pointer text-muted">Relationships</summary>
    {open && <div className="grid gap-2 pt-2">
      <QueryFeedback query={links} />
      <div className="flex flex-wrap gap-2">{links.items.map(link => {
        const item = link.related_item;
        if (!item?.container_id && !item?.collection_id) return null;
        const type = types.data?.find(type => type.id === link.type_id);
        const label = link.directed && link.target_id === itemId ? type?.inverse_label ?? type?.term.name : type?.term.name;
        return <Link key={link.id} className="rounded-full border px-2 py-1 text-accent hover:bg-surface-muted" to="/w/$workspaceId/l/$listId" params={{ workspaceId: item.workspace_id, listId: item.collection_id ?? item.container_id! }} search={{ item: item.id }}>
          {label ?? 'Related'}{link.directed ? link.target_id === itemId ? ' ← ' : ' → ' : ': '}{item.name}
        </Link>;
      })}</div>
      {links.isSuccess && !links.items.length && <p className="text-muted">No related items.</p>}
      {links.hasNextPage && <Button size="sm" disabled={links.isFetchingNextPage} onClick={() => void links.fetchNextPage()}>More related items</Button>}
    </div>}
  </details>;
}
