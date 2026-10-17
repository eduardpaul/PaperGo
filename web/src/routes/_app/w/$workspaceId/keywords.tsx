import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { ArrowLeft, Tag as KeywordIcon } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
import { useState } from 'react';
import { useApiPages } from '@/api/pages';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { QueryFeedback } from '@/features/admin/common';

export const Route = createFileRoute('/_app/w/$workspaceId/keywords')({ component: WorkspaceKeywords });
function WorkspaceKeywords() {
  const { workspaceId } = Route.useParams();
  const workspace = useQuery(resourceQuery(workspaceId));
  const [prefix, setPrefix] = useState('');
  const [keyword, setKeyword] = useState('');
  const keywords = useApiPages<{ keyword: string; name: string; count: number }>(
    ['keywords', workspaceId],
    `/v1/workspaces/${workspaceId}/keywords`,
    { query: { prefix } },
  );
  const matches = useApiPages<Resource>(['resources', workspaceId, 'keyword', keyword], '/v1/resources', {
    enabled: !!keyword,
    query: { workspace_id: workspaceId, keyword },
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
        icon={KeywordIcon}
        title="Keywords"
        description="Keywords used in this workspace. Choose one to see what carries it."
      />
      <Input
        aria-label="Filter keyword prefix"
        placeholder="Filter keywords…"
        value={prefix}
        onChange={(event) => setPrefix(event.target.value)}
      />
      <QueryFeedback query={keywords} />
      <div className="my-3 flex flex-wrap gap-2">
        {keywords.items.map((entry) => (
          <Button
            key={entry.keyword}
            size="sm"
            variant={keyword === entry.keyword ? 'primary' : 'secondary'}
            aria-pressed={keyword === entry.keyword}
            onClick={() => setKeyword(keyword === entry.keyword ? '' : entry.keyword)}
          >
            {entry.name} · {entry.count}
          </Button>
        ))}
      </div>
      {keywords.hasNextPage && (
        <Button onClick={() => void keywords.fetchNextPage()} disabled={keywords.isFetchingNextPage}>
          More keywords
        </Button>
      )}
      {keyword && (
        <>
          <h2 className="mt-5 mb-2 font-medium">{keywords.items.find(entry => entry.keyword === keyword)?.name ?? 'Keyword'}</h2>
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
