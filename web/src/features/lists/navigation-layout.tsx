import { useQuery } from '@tanstack/react-query';
import { ChevronRight, Folder, FolderOpen, Tags, X } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { termsById } from '@/api/queries';
import { navigationItemsRequest, navigationRequest, navigationSelection, noTerm } from '@/api/requests';
import type { FieldDefinition, ListView, NavigationMatch, NavigationNode, NavigationPage, NavigationSelection, Resource, Term } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Card } from '@/components/ui/card';
import { QueryFeedback } from '@/features/admin/common';
import { useValueNames } from '@/features/fields/names';
import { cn } from '@/lib/utils';
import { ItemsTable } from './items-table';

/** A field the view browses as a tree, with the terms opened in the other trees its counts follow. */
interface Tree {
  viewId: string;
  field: string;
  label: string;
  others: NavigationSelection[];
}

/** One level of a tree, kept per parent so the tree, the folders and the notes share it. */
function useLevel(tree: Tree, parentId?: string, enabled = true) {
  const call = navigationRequest(tree.viewId, tree.field, parentId, tree.others);
  return useApiPages<NavigationNode>(['resources', 'navigation', tree.viewId, tree.field, parentId ?? 'roots', tree.others], call.path, {
    query: call.query,
    body: call.body,
    enabled,
  });
}

/** The first page of a level carries the tree's mode and, at the roots, the items without a term. */
function levelInfo(level: ReturnType<typeof useLevel>) {
  return level.data?.pages[0] as NavigationPage | undefined;
}

/** The term an opened selection names, or `none` for the items without one. */
function termOf(selection?: NavigationSelection) {
  return selection?.no_value ? noTerm : selection?.term_id;
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
  tree,
  parentId,
  depth,
  selected,
  expanded,
  subterms,
  onSelect,
  onToggle,
}: {
  tree: Tree;
  parentId?: string;
  depth: number;
  selected?: string;
  expanded: Set<string>;
  subterms: boolean;
  onSelect: (term: string) => void;
  onToggle: (term: string) => void;
}) {
  const level = useLevel(tree, parentId);
  if (level.isPending) return <Skeleton className="m-1 h-6" />;
  if (level.isError) return <QueryFeedback query={level} />;
  if (level.items.length === 0) return <p className={cn('py-1 text-xs text-muted', depth > 0 ? 'pl-8' : 'px-2')}>No terms</p>;
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
              <button
                type="button"
                className={cn('flex min-w-0 flex-1 items-center gap-2 text-left', node.subtree_count === 0 && selected !== node.term_id && 'text-muted')}
                onClick={() => onSelect(node.term_id)}
              >
                {selected === node.term_id ? <FolderOpen className="size-4 shrink-0" /> : <Folder className="size-4 shrink-0" />}
                <span className="truncate">{node.name}</span>
                <Count node={node} subterms={subterms} />
              </button>
            </div>
            {open && node.has_children && (
              <TreeLevel
                tree={tree}
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

/** The opened term's ancestors, root first, for its chip and the tree's open branches. */
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

/** One field's tree with its mode, an entry clearing its term and its "No value" node. */
function FieldTree({ tree, selection, onOpen }: { tree: Tree; selection?: NavigationSelection; onOpen: (term?: string) => void }) {
  const roots = useLevel(tree);
  const info = levelInfo(roots);
  const term = termOf(selection);
  const path = useTermPath(term);
  // The opened term's branch is open unless people closed it; other nodes open when people open them.
  const [overrides, setOverrides] = useState<Map<string, boolean>>(new Map());
  const expanded = new Set(
    [...(path.data?.map((entry) => entry.id) ?? []), ...[...overrides].filter(([, open]) => open).map(([id]) => id)].filter(
      (id) => overrides.get(id) !== false,
    ),
  );
  const toggle = (id: string) => setOverrides((current) => new Map(current).set(id, !expanded.has(id)));
  const select = (id?: string) => {
    onOpen(id);
    if (id && id !== noTerm) setOverrides((current) => new Map(current).set(id, true));
  };
  const subterms = !!info?.include_subterms;
  // Narrow screens stack the trees above the items, so trees start closed there; the header names the opened term.
  const [initiallyOpen] = useState(() => typeof window.matchMedia !== 'function' || window.matchMedia('(min-width: 768px)').matches);
  const opened = selection?.no_value ? `No ${tree.label.toLowerCase()}` : path.data?.at(-1)?.name;
  return (
    <details open={initiallyOpen} className="group rounded-lg border bg-surface p-2 text-sm">
      <summary className="flex cursor-pointer list-none flex-wrap items-center gap-2 px-1">
        <ChevronRight className="size-4 text-muted transition-transform group-open:rotate-90" />
        <Tags className="size-4 text-muted" />
        <span className="font-medium">{tree.label}</span>
        {info && (
          <Badge className="ml-auto" title={subterms ? 'A term shows the items of the terms below it too' : 'A term shows only the items tagged with it'}>
            {subterms ? 'Includes subterms' : 'This term only'}
          </Badge>
        )}
        {selection && (
          <span className="flex w-full items-center gap-2 pl-6 text-xs">
            <span className="truncate font-medium text-accent">{opened ?? '…'}</span>
            <button
              type="button"
              aria-label={`Clear ${tree.label}`}
              className="ml-auto text-muted hover:text-foreground hover:underline"
              onClick={(event) => {
                event.preventDefault();
                select(undefined);
              }}
            >
              Clear
            </button>
          </span>
        )}
      </summary>
      <nav aria-label={`${tree.label} navigation`} className="mt-1">
        {roots.isError ? (
          <QueryFeedback query={roots} />
        ) : (
          <div role="tree" aria-label={tree.label}>
            <TreeLevel
              tree={tree}
              depth={0}
              selected={selection?.term_id}
              expanded={expanded}
              subterms={subterms}
              onSelect={select}
              onToggle={toggle}
            />
          </div>
        )}
        {info?.no_value_count !== undefined && (info.no_value_count > 0 || !info.hide_empty) && (
          <button
            type="button"
            className={cn('mt-1 flex w-full items-center gap-2 rounded px-2 py-0.5 text-left text-muted', term === noTerm && 'bg-accent-soft text-accent')}
            onClick={() => select(noTerm)}
          >
            <Folder className="size-4" /> No {tree.label.toLowerCase()}
            <Badge className="ml-auto">{info.no_value_count}</Badge>
          </button>
        )}
      </nav>
    </details>
  );
}

/** An opened term as a removable chip whose ancestors open the parent terms. */
function SelectionChip({ tree, selection, onOpen }: { tree: Tree; selection: NavigationSelection; onOpen: (term?: string) => void }) {
  const path = useTermPath(selection.term_id);
  return (
    <span className="inline-flex items-center gap-1 rounded-full border bg-surface px-2 py-0.5">
      <span className="text-muted">{tree.label}:</span>
      {selection.no_value ? (
        <span className="font-medium">No {tree.label.toLowerCase()}</span>
      ) : (
        path.data?.map((entry, index) =>
          index === path.data.length - 1 ? (
            <span key={entry.id} className="font-medium">
              {entry.name}
            </span>
          ) : (
            <span key={entry.id} className="flex items-center gap-1">
              <button type="button" className="hover:underline" onClick={() => onOpen(entry.id)}>
                {entry.name}
              </button>
              <ChevronRight className="size-3 text-muted" />
            </span>
          ),
        ) ?? <Skeleton className="h-4 w-16" />
      )}
      <button type="button" aria-label={`Remove ${tree.label}`} className="rounded-full p-0.5 hover:bg-surface-muted" onClick={() => onOpen(undefined)}>
        <X className="size-3" />
      </button>
    </span>
  );
}

/** What an opened term shows: its items alone, or those below it too. */
function SelectionNote({ tree, selection }: { tree: Tree; selection: NavigationSelection }) {
  const path = useTermPath(selection.term_id);
  const subterms = !!levelInfo(useLevel(tree))?.include_subterms;
  const current = path.data?.at(-1);
  const parent = useLevel(tree, path.data && path.data.length > 1 ? path.data.at(-2)!.id : undefined, !!current && !subterms);
  const self = parent.items.find((entry) => entry.term_id === selection.term_id);
  if (selection.no_value) return <li>No {tree.label.toLowerCase()}: the items without a {tree.label.toLowerCase()}.</li>;
  if (!current) return null;
  const below = !subterms && self ? self.subtree_count - self.count : 0;
  return (
    <li>
      <span className="font-medium text-foreground">{current.name}</span>
      {subterms ? ': its items and those tagged with any term below it.' : ': only the items tagged with it'}
      {!subterms && (below > 0 ? `; ${below} more ${below === 1 ? 'is' : 'are'} tagged below it, open a subterm to see ${below === 1 ? 'it' : 'them'}.` : '.')}
    </li>
  );
}

/** The terms below a tree's opened term as folders; at the top level, its roots and the items without a term. */
function Folders({ tree, parentId, onOpen }: { tree: Tree; parentId?: string; onOpen: (term: string) => void }) {
  const roots = useLevel(tree);
  const level = useLevel(tree, parentId);
  const info = levelInfo(roots);
  const subterms = !!info?.include_subterms;
  const noValue = parentId ? 0 : (info?.no_value_count ?? 0);
  if (level.isPending) return <Skeleton className="mb-4 h-16" />;
  if (level.isError) return <QueryFeedback query={level} />;
  if (level.items.length === 0 && !noValue) {
    return parentId ? null : (
      <Card>
        <EmptyState icon={Tags} title={`No ${tree.label.toLowerCase()} yet`}>
          Tag items with {tree.label.toLowerCase()} to browse them here.
        </EmptyState>
      </Card>
    );
  }
  return (
    <div className="mb-4">
      <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted">{parentId ? 'Subterms' : `Browse by ${tree.label.toLowerCase()}`}</h3>
      <ul aria-label={parentId ? 'Subterms' : `${tree.label} terms`} className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {level.items.map((entry) => (
        <li key={entry.term_id}>
          <button
            type="button"
            className={cn('flex w-full items-center gap-2 rounded-lg border bg-surface px-3 py-2 text-left hover:bg-surface-muted', entry.subtree_count === 0 && 'text-muted')}
            onClick={() => onOpen(entry.term_id)}
          >
            <Folder className="size-4 shrink-0 text-muted" />
            <span className="truncate font-medium">{entry.name}</span>
            <Count node={entry} subterms={subterms} />
          </button>
        </li>
      ))}
      {noValue > 0 && (
        <li>
          <button
            type="button"
            className="flex w-full items-center gap-2 rounded-lg border border-dashed bg-surface px-3 py-2 text-left text-muted hover:bg-surface-muted"
            onClick={() => onOpen(noTerm)}
          >
            <Folder className="size-4 shrink-0" />
            <span className="truncate">No {tree.label.toLowerCase()}</span>
            <Badge className="ml-auto">{noValue}</Badge>
          </button>
        </li>
      )}
      </ul>
      {!parentId && <p className="mt-3 text-sm text-muted">Open a term to see its items.</p>}
    </div>
  );
}

function NodeItems({
  view,
  selected,
  match,
  onBack,
  fields,
  columns,
  publishing,
  activeId,
  renderItem,
  onOpen,
}: {
  view: ListView;
  selected: NavigationSelection[];
  match: NavigationMatch;
  /** Closes the latest opened term. */
  onBack: () => void;
  fields: FieldDefinition[];
  columns: string[];
  publishing: boolean;
  activeId?: string;
  renderItem?: (item: Resource, element: ReactElement) => ReactNode;
  onOpen: (item: Resource) => void;
}) {
  const call = navigationItemsRequest(view.id, selected);
  const items = useApiPages<Resource>(['resources', view.container_id, 'navigation-items', view.id, selected], call.path, {
    query: call.query,
    body: call.body,
  });
  const names = useValueNames(fields, items.items);
  if (items.isPending) return <Skeleton className="h-24" />;
  if (items.isError) return <QueryFeedback query={items} />;
  const first = items.data?.pages[0];
  if (items.items.length === 0) {
    return (
      <Card>
        <EmptyState icon={FolderOpen} title={selected.length > 1 && match === 'all' ? 'No items have all of these terms' : 'No items here'}>
          {selected.length > 1 && match === 'all' ? 'Remove a term to see more items.' : 'Open another term to see its items.'}
          <div className="mt-3">
            <Button size="sm" onClick={onBack}>
              {selected.length > 1 ? 'Remove the last opened term' : 'Clear term'}
            </Button>
          </div>
        </EmptyState>
      </Card>
    );
  }
  return (
    <>
      {first?.total !== undefined && (
        <p role="status" className="mb-2 text-xs text-muted">
          {first.total}
          {first.total_capped ? '+' : ''} {first.total === 1 && !first.total_capped ? 'item' : 'items'}
        </p>
      )}
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
 * Browses a view's taxonomy and keywords fields as folders: a term tree per field on the side; the opened terms,
 * the subterms of the latest one as folders and the items matching them. Whether a term also shows the items below
 * it follows each field, and whether items match all opened terms or any of them follows the view.
 */
export function ItemsNavigation({
  view,
  fields,
  columns,
  publishing,
  selected,
  activeId,
  onSelect,
  renderItem,
  onOpen,
}: {
  view: ListView;
  fields: FieldDefinition[];
  columns: string[];
  publishing: boolean;
  /** The terms opened in the trees, the latest last. */
  selected: NavigationSelection[];
  activeId?: string;
  onSelect: (selected: NavigationSelection[]) => void;
  renderItem?: (item: Resource, element: ReactElement) => ReactNode;
  onOpen: (item: Resource) => void;
}) {
  const match = view.settings?.match ?? 'all';
  const trees: Tree[] = (view.settings?.fields ?? []).map(({ field }) => ({
    viewId: view.id,
    field,
    label: field === '$keywords' ? 'Keywords' : (fields.find((entry) => entry.key === field)?.label ?? field),
    // Matching all, a tree counts within the terms opened in the others; matching any, those add items instead.
    others: match === 'all' ? selected.filter((entry) => entry.field !== field) : [],
  }));
  const opened = selected.filter((entry) => trees.some((tree) => tree.field === entry.field));
  const open = (field: string, term?: string) =>
    onSelect([...opened.filter((entry) => entry.field !== field), ...(term ? [navigationSelection(field, term)] : [])]);
  const latest = opened.at(-1);
  const folderTree = trees.find((tree) => tree.field === (latest?.field ?? trees[0]?.field));
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
      <div className="grid content-start gap-3">
        {trees.map((tree) => (
          <FieldTree key={tree.field} tree={tree} selection={opened.find((entry) => entry.field === tree.field)} onOpen={(term) => open(tree.field, term)} />
        ))}
      </div>
      <section aria-live="polite" className="min-w-0">
        {opened.length > 0 && (
          <div className="mb-3 grid gap-2 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-muted">{opened.length > 1 ? `Items matching ${match} of` : 'Items in'}</span>
              <ul aria-label="Opened terms" className="flex flex-wrap items-center gap-2">
                {opened.map((selection, index) => {
                  const tree = trees.find((entry) => entry.field === selection.field)!;
                  return (
                    <li key={selection.field} className="flex items-center gap-2">
                      {index > 0 && <span className="text-xs font-medium uppercase text-muted">{match === 'all' ? 'and' : 'or'}</span>}
                      <SelectionChip tree={tree} selection={selection} onOpen={(term) => open(tree.field, term)} />
                    </li>
                  );
                })}
              </ul>
              {opened.length > 1 && (
                <Button size="sm" variant="ghost" onClick={() => onSelect([])}>
                  Clear all
                </Button>
              )}
            </div>
            <ul className="grid gap-0.5 text-muted">
              {opened.map((selection) => (
                <SelectionNote key={selection.field} tree={trees.find((entry) => entry.field === selection.field)!} selection={selection} />
              ))}
            </ul>
          </div>
        )}
        {folderTree && !latest?.no_value && (
          <Folders tree={folderTree} parentId={latest?.term_id} onOpen={(term) => open(folderTree.field, term)} />
        )}
        {opened.length > 0 ? (
          <NodeItems
            view={view}
            selected={opened}
            match={match}
            onBack={() => onSelect(opened.slice(0, -1))}
            fields={fields}
            columns={columns}
            publishing={publishing}
            activeId={activeId}
            renderItem={renderItem}
            onOpen={onOpen}
          />
        ) : null}
      </section>
    </div>
  );
}
