import { createFileRoute, Link } from '@tanstack/react-router';
import { useState } from 'react';
import { useApiPages } from '@/api/pages';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { QueryFeedback } from '@/features/admin/common';

export const Route = createFileRoute('/_app/w/$workspaceId/tags')({ component: WorkspaceTags });
function WorkspaceTags() {
  const { workspaceId } = Route.useParams();
  const [prefix, setPrefix] = useState('');
  const [tag, setTag] = useState('');
  const tags = useApiPages<{ tag: string; count: number }>(
    ['tags', workspaceId],
    `/v1/workspaces/${workspaceId}/tags`,
    { query: { prefix } },
  );
  const matches = useApiPages<Resource>(['resources', workspaceId, 'tag', tag], '/v1/resources', {
    enabled: !!tag,
    query: { workspace_id: workspaceId, tag },
  });
  return (
    <Page>
      <PageHeader
        title="Workspace tags"
        description="Labels in this workspace and the resources you can read that carry them."
      />
      <Input
        aria-label="Filter tag prefix"
        placeholder="Filter tags…"
        value={prefix}
        onChange={(event) => setPrefix(event.target.value)}
      />
      <QueryFeedback query={tags} />
      <div className="my-3 flex flex-wrap gap-2">
        {tags.items.map((entry) => (
          <Button key={entry.tag} aria-pressed={tag === entry.tag} onClick={() => setTag(entry.tag)}>
            {entry.tag} · {entry.count}
          </Button>
        ))}
      </div>
      {tags.hasNextPage && (
        <Button onClick={() => void tags.fetchNextPage()} disabled={tags.isFetchingNextPage}>
          More tags
        </Button>
      )}
      {tag && (
        <>
          <h2 className="mt-5 mb-2 font-medium">{tag}</h2>
          <QueryFeedback query={matches} />
          <ul className="divide-y">
            {matches.items.map((item) => (
              <li key={item.id} className="py-2">
                {item.kind === 'workspace' ? (
                  <Link to="/w/$workspaceId" params={{ workspaceId: item.id }}>
                    {item.name}
                  </Link>
                ) : (
                  <Link
                    to="/w/$workspaceId/l/$listId"
                    params={{ workspaceId, listId: item.container_id ?? item.id }}
                    search={
                      item.kind === 'item' ? { item: item.id } : item.kind === 'folder' ? { folder: item.id } : {}
                    }
                  >
                    {item.name}
                  </Link>
                )}
              </li>
            ))}
          </ul>
          {matches.hasNextPage && (
            <Button onClick={() => void matches.fetchNextPage()} disabled={matches.isFetchingNextPage}>
              More resources
            </Button>
          )}
        </>
      )}
    </Page>
  );
}
