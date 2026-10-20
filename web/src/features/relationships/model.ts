export interface Endpoint {
  id: string;
  name: string;
  workspace_id: string;
  workspace_name: string;
  collection_id: string;
  collection_name: string;
}

export interface Connection {
  id: string;
  source_id: string;
  target_id: string;
  type_id?: string;
  type_name: string;
  inverse_label?: string;
  directed: boolean;
  created_at: string;
  source_item: Endpoint;
  target_item: Endpoint;
}

export interface ConnectionType {
  id: string;
  name: string;
  directed: boolean;
  inverse_label?: string;
}

export interface Neighborhood {
  nodes: Endpoint[];
  edges: Connection[];
  truncated: boolean;
}

export interface ExplorerSearch {
  type?: string;
  item?: string;
  collection?: string;
  scope?: 'internal' | 'external';
  direction?: 'incoming' | 'outgoing' | 'symmetric';
  view?: 'graph';
  edge?: string;
  trail?: string[];
}

export function explorerQuery(search: ExplorerSearch) {
  return {
    type: search.type === 'untyped' ? undefined : search.type,
    untyped: search.type === 'untyped' ? 'true' : undefined,
    item_id: search.item,
    collection_id: search.collection,
    scope: search.scope,
    direction: search.direction,
  };
}

export function locationLabel(item: Endpoint, workspaceId: string) {
  return item.workspace_id === workspaceId ? item.collection_name : `${item.workspace_name} / ${item.collection_name}`;
}

/** Direction is always relative to the focus; symmetric storage order has no meaning. */
export function connectionLabel(edge: Connection, focus?: string) {
  if (!edge.directed) return edge.type_name;
  return edge.target_id === focus && edge.inverse_label ? edge.inverse_label : edge.type_name;
}

export function peerOf(edge: Connection, focus: string) {
  return edge.source_id === focus ? edge.target_item : edge.source_item;
}
