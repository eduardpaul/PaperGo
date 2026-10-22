import { useQuery } from '@tanstack/react-query';
import { ChevronRight, Folder, FolderOpen, Tags } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { termsById } from '@/api/queries';
import { navigationItemsRequest, navigationRequest, noTerm } from '@/api/requests';
import type { FieldDefinition, ListView, NavigationNode, NavigationPage, Resource, Term } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Card } from '@/components/ui/card';
import { QueryFeedback } from '@/features/admin/common';
import { useValueNames } from '@/features/fields/names';
import { cn } from '@/lib/utils';
import { ItemsTable } from './items-table';

/** One level of a navigation view, kept per parent so the tree and the folder list share it. */
function useLevel(viewId: string, parentId?: string, enabled = true) {
  const call = navigationRequest(viewId, parentId);
  return useApiPages<NavigationNode>(['resources', 'navigation', viewId, parentId ?? 'roots'], call.path, {
    query: call.query,
    body: call.body,
    enabled,
  });
}

/** The first page of a level carries the view's mode and, at the roots, the items without a term. */
function levelInfo(level: ReturnType<typeof useLevel>) {
  return level.data?.pages[0] as NavigationPage | undefined;
}

function Count({ node, subterms }: { node: NavigationNode; subterms: boolean }) {
  const below = node.subtree_count - node.count;
  return (
    <span className="ml-auto flex shrink-0 items-center gap-1">
      <Badge aria-label={`${node.count} items`}>{node.count}</Badge>
      {!subterms && below > 0 && (
        <span className="text-xs text-muted" title={`${below} more items hold a term below ${node.name}`}>
          +{below} below
        </span>
      )}
    </span>
  );
}

function TreeLevel({
  viewId,
  parentId,
  depth,
  selected,
  expanded,
  subterms,
  onSelect,
  onToggle,
}: {
  viewId: string;
  parentId?: string;
  depth: number;
  selected?: string;
  expanded: Set<string>;
  subterms: boolean;
  onSelect: (term: string) => void;
  onToggle: (term: string) => void;
}) {
  const level = useLevel(viewId, parentId);
  if (level.isPending) return <Skeleton className="m-1 h-6" />;
  if (level.isError) return <QueryFeedback query={level} />;
  if (level.items.length === 0 && depth > 0) return <p className="py-1 pl-8 text-xs text-muted">No terms</p>;
  return (
    <ul role="group" className={cn(depth > 0 && 'ml-3 border-l pl-1')}>
      {level.items.map((node) => {
        const open = expanded.has(node.term_id);
        return (
          <li key={node.term_id} role="treeitem" aria-expanded={node.has_children ? open : undefined} aria-selected={selected === node.term_id}>
            <div className={cn('flex items-center gap-1 rounded px-1 py-0.5', selected === node.term_id && 'bg-accent-soft text-accent')}>
              {node.has_children ? (
                <button
                  type="button"
                  aria-label={`${open ? 'Collapse' : 'Expand'} ${node.name}`}
                  className="rounded p-0.5 hover:bg-surface-muted"
                  onClick={() => onToggle(node.term_id)}
                >
                  <ChevronRight className={cn('size-4 transition-transform', open && 'rotate-90')} />
                </button>
              ) : (
                <span className="w-5" />
              )}
              <button type="button" className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => onSelect(node.term_id)}>
                {selected === node.term_id ? <FolderOpen className="size-4 shrink-0" /> : <Folder className="size-4 shrink-0" />}
                <span className="truncate">{node.name}</span>
                <Count node={node} subterms={subterms} />
              </button>
            </div>
            {open && (
              <TreeLevel
                viewId={viewId}
                parentId={node.term_id}
                depth={depth + 1}
                selected={selected}
                expanded={expanded}
                subterms={subterms}
                onSelect={onSelect}
                onToggle={onToggle}
              />
            )}
          </li>
        );
      })}
      {level.hasNextPage && (
        <li>
          <Button size="sm" variant="ghost" disabled={level.isFetchingNextPage} onClick={() => void level.fetchNextPage()}>
            {level.isFetchingNextPage ? 'Loading…' : 'More terms'}
          </Button>
        </li>
      )}
    </ul>
  );
}

/** The selected term's ancestors, root first, for the breadcrumb and the tree's open branches. */
function useTermPath(term?: string) {
  return useQuery({
    queryKey: ['terms', term, 'path'],
    enabled: !!term && term !== noTerm,
    queryFn: async () => {
      const current = (await api<Term>(`/v1/terms/${term}`)).data;
      const ids = (current.path ?? '').split('/').filter(Boolean);
      const named = new Map((await termsById(ids)).map((entry) => [entry.id, entry.name]));
      return ids.map((id) => ({ id, name: named.get(id) ?? (id === current.id ? current.name : id) }));
    },
  });
}

function NodeItems({
  view,
  term,
  fields,
  columns,
  publishing,
  activeId,
  renderItem,
  onOpen,
}: {
  view: ListView;
  term: string;
  fields: FieldDefinition[];
  columns: string[];
  publishing: boolean;
  activeId?: string;
  renderItem?: (item: Resource, element: ReactElement) => ReactNode;
  onOpen: (item: Resource) => void;
}) {
  const call = navigationItemsRequest(view.id, term);
  const items = useApiPages<Resource>(['resources', view.container_id, 'navigation-items', view.id, term], call.path, {
    query: call.query,
    body: call.body,
  });
  const names = useValueNames(fields, items.items);
  if (items.isPending) return <Skeleton className="h-24" />;
  if (items.isError) return <QueryFeedback query={items} />;
  if (items.items.length === 0) return <p className="text-sm text-muted">No items here.</p>;
  return (
    <>
      <ItemsTable
        renderRow={renderItem}
        rows={items.items}
        columns={columns}
        fields={fields}
        publishing={publishing}
        names={names}
        selectable={false}
        selected={new Map()}
        activeId={activeId}
        sortable={new Set()}
        onSort={() => undefined}
        onToggle={() => undefined}
        onToggleAll={() => undefined}
        onOpen={onOpen}
      />
      {items.hasNextPage && (
        <Button className="mt-2" size="sm" variant="ghost" disabled={items.isFetchingNextPage} onClick={() => void items.fetchNextPage()}>
          {items.isFetchingNextPage ? 'Loading…' : 'Load more'}
        </Button>
      )}
    </>
  );
}

/**
 * Browses a taxonomy or keywords field as folders: the term tree on the side, the opened term's subterms as
 * folders and its items. Whether a term also shows the items below it follows the view.
 */
export function ItemsNavigation({
  view,
  fields,
  columns,
  publishing,
  term,
  activeId,
  onTerm,
  renderItem,
  onOpen,
}: {
  view: ListView;
  fields: FieldDefinition[];
  columns: string[];
  publishing: boolean;
  /** The opened term, `none` for items without one, or nothing for the top level. */
  term?: string;
  activeId?: string;
  onTerm: (term: string | undefined) => void;
  renderItem?: (item: Resource, element: ReactElement) => ReactNode;
  onOpen: (item: Resource) => void;
}) {
  const roots = useLevel(view.id);
  const info = levelInfo(roots);
  const path = useTermPath(term);
  const opened = term && term !== noTerm ? term : undefined;
  const children = useLevel(view.id, opened, !!opened);
  // The opened term's branch is open unless people closed it; other nodes open when people open them.
  const [overrides, setOverrides] = useState<Map<string, boolean>>(new Map());
  const expanded = new Set(
    [...(path.data?.map((entry) => entry.id) ?? []), ...[...overrides].filter(([, open]) => open).map(([id]) => id)].filter(
      (id) => overrides.get(id) !== false,
    ),
  );
  const toggle = (id: string) => setOverrides((current) => new Map(current).set(id, !expanded.has(id)));
  const select = (id: string | undefined) => {
    onTerm(id);
    if (id && id !== noTerm) setOverrides((current) => new Map(current).set(id, true));
  };
  const field = view.settings?.field === '$keywords' ? 'Keywords' : (fields.find((entry) => entry.key === view.settings?.field)?.label ?? view.settings?.field);
  const subterms = !!info?.include_subterms;
  const folders = opened ? children : roots;
  const current = path.data?.at(-1);
  const parentLevel = useLevel(view.id, path.data && path.data.length > 1 ? path.data.at(-2)!.id : undefined, !!opened && !!path.data);
  const self = parentLevel.items.find((entry) => entry.term_id === opened);
  if (roots.isError) return <QueryFeedback query={roots} />;
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
      <nav aria-label={`${field ?? 'Terms'} navigation`} className="rounded-lg border bg-surface p-2 text-sm">
        <div className="mb-2 flex items-center gap-2 px-1">
          <Tags className="size-4 text-muted" />
          <span className="font-medium">{field}</span>
          <Badge className="ml-auto" title={subterms ? 'A term shows the items of the terms below it too' : 'A term shows only the items tagged with it'}>
            {subterms ? 'Includes subterms' : 'This term only'}
          </Badge>
        </div>
        <button
          type="button"
          className={cn('mb-1 flex w-full items-center gap-2 rounded px-2 py-0.5 text-left', !term && 'bg-accent-soft text-accent')}
          onClick={() => select(undefined)}
        >
          <FolderOpen className="size-4" /> All terms
        </button>
        <div role="tree" aria-label={field}>
          <TreeLevel
            viewId={view.id}
            depth={0}
            selected={opened}
            expanded={expanded}
            subterms={subterms}
            onSelect={select}
            onToggle={toggle}
          />
        </div>
        {info?.no_value_count !== undefined && (info.no_value_count > 0 || !info.hide_empty) && (
          <button
            type="button"
            className={cn('mt-1 flex w-full items-center gap-2 rounded px-2 py-0.5 text-left text-muted', term === noTerm && 'bg-accent-soft text-accent')}
            onClick={() => select(noTerm)}
          >
            <Folder className="size-4" /> No {field?.toLowerCase() ?? 'value'}
            <Badge className="ml-auto">{info.no_value_count}</Badge>
          </button>
        )}
      </nav>
      <section aria-live="polite" className="min-w-0">
        <nav aria-label="Term path" className="mb-2 flex flex-wrap items-center gap-1 text-sm">
          <button type="button" className="hover:underline" onClick={() => select(undefined)}>
            All terms
          </button>
          {term === noTerm && (
            <>
              <ChevronRight className="size-3 text-muted" /> <span aria-current="page">No {field?.toLowerCase() ?? 'value'}</span>
            </>
          )}
          {path.data?.map((entry, index) => (
            <span key={entry.id} className="flex items-center gap-1">
              <ChevronRight className="size-3 text-muted" />
              {index === path.data.length - 1 ? (
                <span aria-current="page" className="font-medium">
                  {entry.name}
                </span>
              ) : (
                <button type="button" className="hover:underline" onClick={() => select(entry.id)}>
                  {entry.name}
                </button>
              )}
            </span>
          ))}
        </nav>
        {opened && current && (
          <p className="mb-3 text-sm text-muted">
            {subterms ? `Items tagged ${current.name} or any term below it` : `Items tagged ${current.name}`}
            {!subterms && self && self.subtree_count > self.count && (
              <> · {self.subtree_count - self.count} more are tagged with terms below it; open a subterm to see them.</>
            )}
          </p>
        )}
        {term === noTerm && <p className="mb-3 text-sm text-muted">Items without a {field?.toLowerCase() ?? 'value'}.</p>}
        {term !== noTerm && folders.items.length > 0 && (
          <ul aria-label="Subterms" className="mb-4 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {folders.items.map((entry) => (
              <li key={entry.term_id}>
                <button
                  type="button"
                  className="flex w-full items-center gap-2 rounded-lg border bg-surface px-3 py-2 text-left hover:bg-surface-muted"
                  onClick={() => select(entry.term_id)}
                >
                  <Folder className="size-4 shrink-0 text-muted" />
                  <span className="truncate font-medium">{entry.name}</span>
                  <Count node={entry} subterms={subterms} />
                </button>
              </li>
            ))}
          </ul>
        )}
        {term ? (
          <NodeItems
            view={view}
            term={term}
            fields={fields}
            columns={columns}
            publishing={publishing}
            activeId={activeId}
            renderItem={renderItem}
            onOpen={onOpen}
          />
        ) : (
          roots.isPending ? (
            <Skeleton className="h-24" />
          ) : roots.items.length === 0 ? (
            <Card>
              <EmptyState icon={Tags} title={`No ${field?.toLowerCase() ?? 'terms'} yet`}>
                Tag items with {field?.toLowerCase() ?? 'terms'} to browse them here.
              </EmptyState>
            </Card>
          ) : (
            <p className="text-sm text-muted">Open a term to see its items.</p>
          )
        )}
      </section>
    </div>
  );
}
