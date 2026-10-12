import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { ArrowLeft, Tag } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
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
  const workspace = useQuery(resourceQuery(workspaceId));
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
      <Link
        to="/w/$workspaceId"
        params={{ workspaceId }}
        className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground"
      >
        <ArrowLeft className="size-3.5" /> {workspace.data?.name ?? 'Workspace'}
      </Link>
      <PageHeader
        icon={Tag}
        title="Tags"
        description="Tags used in this workspace. Choose one to see what carries it."
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
          <Button
            key={entry.tag}
            size="sm"
            variant={tag === entry.tag ? 'primary' : 'secondary'}
            aria-pressed={tag === entry.tag}
            onClick={() => setTag(tag === entry.tag ? '' : entry.tag)}
          >
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
