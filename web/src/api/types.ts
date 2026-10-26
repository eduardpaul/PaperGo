import type { Trigger } from '@/features/workflows/model';
import type { FormSchema } from '@/features/workflows/model';

export type ArchiveSelection = 'active' | 'archived' | 'all';

export type Kind = 'workspace' | 'list' | 'library' | 'folder' | 'item';

export type AccessAction = 'read' | 'read_draft' | 'write' | 'publish' | 'manage';

export interface Page<T> {
  value?: T[] | null;
  '@odata.nextLink'?: string | null;
  total?: number;
  /** More than `total` items match; counting stopped there. */
  total_capped?: boolean;
  field?: string;
  by?: string;
}

export interface Resource {
  archived?: boolean;
  archived_at?: string;
  archived_by?: string;
  id: string;
  workspace_id: string;
  personal_owner_id?: string;
  system_key?: string;
  parent_id?: string;
  container_id?: string;
  content_type_id?: string;
  kind: Kind;
  name: string;
  description?: string;
  default_currency?: string;
  allow_folders?: boolean;
  template_keys?: string[] | null;
  keywords?: string[] | null;
  values?: Record<string, unknown> | null;
  /** The file of a library item's selected revision, the one the caller reads. */
  file?: FileSummary | null;
  inherit_permissions?: boolean;
  version: number;
  head_revision_id?: string;
  published_revision_id?: string;
  publishing_enabled?: boolean;
  versioning?: 'off' | 'major';
  max_versions?: number;
  webdav_enabled?: boolean;
  duplicate_policy?: 'allow' | 'warn' | 'block';
  created_at?: string;
  updated_at?: string;
  created_by?: string;
  updated_by?: string;
  deleted_at?: string;
  deleted_by?: string;
  /** The caller's allowed actions; present on GET and PATCH /v1/resources/{id}. */
  access?: AccessAction[];
}

/** The organization's trusted hosts (GET /v1/organization/settings/trusted-hosts). */
export interface TrustedHosts {
  /** Host names, *.domain wildcards, or * for every public host. */
  hosts: string[];
  version: number;
}

/** The file an item shows: what a viewer needs to pick a handler. */
export interface FileSummary {
  blob_id: string;
  content_type: string;
  filename: string;
  size: number;
  version: number;
}

/** A hit of global search (GET /v1/search). */
export interface SearchHit {
  archived?: boolean;
  archived_at?: string;
  archived_by?: string;
  id: string;
  kind: 'item' | 'folder';
  surface: 'head' | 'published';
  workspace_id: string;
  collection_id: string;
  content_type_id?: string;
  name: string;
  /** The file of a library item hit, on the hit's surface. */
  file?: FileSummary | null;
  snippet?: string;
  /** UTF-8 byte ranges of matches in snippet. */
  highlights?: [number, number][];
  /** The page of the file the snippet is on, when the file's text matched. */
  page?: number;
  /** How the hit was found: by its words, by meaning (semantic search), or both. */
  matched_by: ('keyword' | 'semantic')[];
  rank: number;
  modified_at: string;
  modified_by: string;
}

export interface SearchFacetValue {
  value: string;
  count: number;
}

export interface SearchFacets {
  workspace: SearchFacetValue[];
  collection: SearchFacetValue[];
  content_type: SearchFacetValue[];
  term: SearchFacetValue[];
  keyword: SearchFacetValue[];
}

export interface SearchPage extends Page<SearchHit> {
  /** Some matches were left out because the caller may no longer read them. */
  total_estimated?: boolean;
  facets?: SearchFacets;
  /** The mode the search ran in: keyword when semantic search is off or failed. */
  mode: 'keyword' | 'semantic' | 'hybrid';
}

export interface FieldOptions {
  unique?: boolean;
  description?: string;
  default_value?: unknown;
  max_length?: number;
  minimum?: string;
  maximum?: string;
  currency_code?: string;
  multiple?: boolean;
  lookup_container_id?: string;
  term_set_id?: string;
  /** Picking a term also applies its parent terms (term and keywords fields). */
  apply_parents?: boolean;
  /** Where the field's text goes in search; empty is normal. */
  search_weight?: 'none' | 'normal' | 'high';
}

export interface FieldDefinition {
  id: string;
  key: string;
  label: string;
  type: string;
  options?: FieldOptions;
  indexed?: boolean;
  index_status?: string;
  scale?: number;
  required?: boolean;
  choices?: string[] | null;
  version?: number;
}

/**
 * A catalog type, or a list type: the copy of one catalog type (parent_id)
 * that a list or library customizes. field_ids, hidden_field_ids and rules are
 * the type's own; effective adds everything its ancestors define.
 */
export interface ContentType {
  id: string;
  key: string;
  name: string;
  parent_id?: string | null;
  collection_id?: string | null;
  ancestor_ids?: string[];
  field_ids?: string[] | null;
  hidden_field_ids?: string[] | null;
  effective?: { field_ids: string[]; hidden_field_ids: string[]; rules: ValidationRule[] };
  is_default?: boolean;
  version?: number;
  rules?: ValidationRule[];
}

/** A catalog type listed for search pickers (GET /v1/search/content-types). */
export interface SearchContentType {
  id: string;
  key: string;
  name: string;
  parent_id?: string;
}

export interface ValidationRule {
  key: string;
  field: string;
  op: string;
  other_field?: string;
  when_field?: string;
  when_value?: unknown;
  message: string;
}

export interface ListView {
  system_key?: 'archived';
  id: string;
  container_id: string;
  name: string;
  columns?: string[] | null;
  layout?: string;
  /** Layout settings: the fields a navigation view browses. */
  settings?: ViewSettings;
  is_default?: boolean;
  version: number;
  query?: QuerySpec;
}

export interface ViewSettings {
  /** The fields a navigation view browses, one tree each. */
  fields?: NavigationFieldSetting[];
  /** Whether items match every term opened in the trees or any of them; unset matches all. */
  match?: NavigationMatch;
  /** Leave out terms no item holds at or below them. */
  hide_empty?: boolean;
}

export type NavigationMatch = 'all' | 'any';

export interface NavigationFieldSetting {
  /** `$keywords` or a term or keywords field. */
  field: string;
  /** Items below a term show at it too; unset follows the field. */
  include_subterms?: boolean;
}

/** A term opened in one of a navigation view's trees, or the items holding none of its terms. */
export interface NavigationSelection {
  field: string;
  term_id?: string;
  no_value?: boolean;
}

/** A term of a navigation view's level with the items it lists and those at or below it. */
export interface NavigationNode {
  term_id: string;
  name: string;
  has_children: boolean;
  count: number;
  subtree_count: number;
}

export interface NavigationPage extends Page<NavigationNode> {
  field: string;
  term_set_id: string;
  include_subterms: boolean;
  hide_empty: boolean;
  parent_id?: string;
  no_value_count?: number;
}

export interface FilterExpr {
  and?: FilterExpr[];
  or?: FilterExpr[];
  not?: FilterExpr;
  field?: string;
  op?: string;
  value?: unknown;
  value_ref?: string;
}
export interface QuerySpec {
  archive?: ArchiveSelection;
  filter?: FilterExpr;
  sort?: { field: string; direction: string }[];
  group_by?: string;
  content_type_id?: string;
  parent_id?: string;
  /** Anywhere below this folder: in it or its subfolders. */
  ancestor_id?: string;
  search?: string;
  keyword?: string;
}

export interface Me {
  subject: string;
  display_name: string;
  email: string;
  active: boolean;
  organization_id: string;
  organization_identifier: string;
  scopes?: string[] | null;
}

export interface Preferences {
  default_currency: string;
  language: string;
  time_zone: string;
  date_format: string;
  time_format: '24h' | '12h';
  number_format: string;
  theme: 'system' | 'light' | 'dark';
  document_languages: string;
  inherited?: string[] | null;
  version: number;
}

export interface Home {
  workspace_id: string;
  documents_library_id: string;
  inbox_library_id: string;
}

export interface Inbox {
  kind: string;
  workspace_id: string;
  library_id: string;
  library_name: string;
  group_id?: string | null;
  group_name?: string | null;
}

export interface UserProfile {
  subject: string;
  display_name: string;
  email: string;
  active: boolean;
  created_at?: string;
  deleted_at?: string;
}

export interface CollectionTemplate {
  key: string;
  name: string;
  description: string;
  kind: 'list' | 'library';
}

export interface WorkspaceMember {
  subject: string;
  role: 'owner' | 'member' | 'visitor' | string;
}

export interface Revision {
  id: string;
  revision_number: number;
  name: string;
  created_at: string;
  created_by: string;
  blob_id?: string;
  is_current: boolean;
  changed_fields?: string[] | null;
}

export interface Relationship {
  id: string;
  source_id: string;
  target_id: string;
  type_id: string;
  directed?: boolean;
  related_item?: Resource & { workspace_name?: string; collection_id?: string; collection_name?: string };
}

export interface PermissionGrant {
  /** Workspace role principals stand for everyone with that role in the workspace. */
  principal_type: 'user' | 'group' | 'workspace_visitor' | 'workspace_member' | 'workspace_owner';
  principal_id: string;
  action: string;
  effect: string;
}

export interface Permissions {
  inherit: boolean;
  grants?: PermissionGrant[] | null;
  effective_grants?: PermissionGrant[] | null;
  scope_id?: string;
}

export interface SmartFolder {
  id: string;
  workspace_id?: string;
  owner_id?: string;
  name: string;
  description?: string;
  definition: SmartFolderDefinition;
  version: number;
}

export interface SmartFolderDefinition {
  archive?: ArchiveSelection;
  mode: 'query' | 'classification';
  collection_kind?: 'list' | 'library';
  collections?: string[];
  templates?: string[];
  content_types?: string[];
  terms?: string[];
  term_match?: string;
  filter?: FilterExpr;
  group_by?: { field: string; by?: string }[];
  include_folders?: boolean;
}

export interface SmartFolderEntry {
  collection_kind: 'list' | 'library';
  workspace_id: string;
  collection_id: string;
  collection_name: string;
  item: Resource;
}

/** A value of a query's grouping field and how many matching items hold it. */
export interface QueryGroup {
  value: unknown;
  label?: string;
  count: number;
}

export interface SmartFolderGroup {
  value: unknown;
  label: string;
  count: number;
}

export interface Term {
  id: string;
  name: string;
  term_set_id?: string;
  parent_id?: string | null;
  path?: string;
}

export interface Workflow {
  id: string;
  workspace_id: string;
  key: string;
  name: string;
  description: string;
  execution_author?: string;
  automatic_enabled: boolean;
  manual_enabled: boolean;
  state: 'draft' | 'published' | 'archived';
  automatic_provider: boolean;
  builtin_key?: string;
  role?: string;
  collection_id?: string;
  parameters?: Record<string, unknown>;
  current_version: number;
  /** The current version's TypeScript script. */
  source: string;
  /** What the current version's script declares: its triggers and forms. */
  definition: unknown;
  version: number;
}

export interface WorkflowAvailability { can_run: boolean; code: string; reason: string; manual_mode: string; version: number }
export interface WorkflowAction { workflow?: Workflow; builtin?: BuiltInState; availability: WorkflowAvailability }

export interface BuiltInState {
  manual_mode?: 'none' | 'single' | 'selection' | 'standalone';
  manual_media_types?: string[];
  automatic_triggers?: Trigger[];

  key: string;
  name: string;
  description: string;
  per_collection?: boolean;
  /** Automatic role providers belong to one collection; manual catalog use is independent. */
  role?: string;
  default?: boolean;
  system?: boolean;
  parameters_schema?: {
    properties?: Record<string, { type?: string; title?: string; description?: string; default?: unknown }>;
  };
  workflows?: Workflow[] | null;
}

/** One recorded step of a run's script: an API request, a wait or a value it read. */
export interface WorkflowRunStep {
  seq: number;
  kind: 'request' | 'wait' | 'value';
  name: string;
  status: 'pending' | 'completed' | 'failed';
  input?: unknown;
  output?: unknown;
  /** The output's size in bytes; a finished run drops large outputs and keeps this. */
  size?: number;
  operations?: string[];
  error?: string;
  started_at?: string;
  completed_at?: string;
}

export interface WorkflowRun {
  id: string;
  workflow_id: string;
  workspace_id: string;
  workflow_version?: number;
  status: string;
  event_type?: string;
  item_id?: string;
  items?: string[];
  retry_of?: string;
  /** The run that called this one with ctx.call. */
  parent_run_id?: string;
  /** How many times the run continued as new. */
  generation?: number;
  /** What the last generation carried into the current one. */
  state?: unknown;
  error?: string;
  created_at: string;
  updated_at?: string;
  started_at?: string;
  completed_at?: string;
  /** When a sleeping run continues. */
  waiting_until?: string;
  actor?: string;
  inputs?: Record<string, unknown>;
  result?: unknown;
  steps?: WorkflowRunStep[];
  logs?: { level: 'info' | 'warn' | 'error'; message: string; at: string }[];
}

export interface AccessToken {
  subject: string;
  integration: boolean;
  created_by: string;
  revoked_at?: string;
  id: string;
  name: string;
  prefix: string;
  scopes?: string[] | null;
  /** Workspaces, lists and libraries the token is limited to; empty reaches everything. */
  resources?: string[] | null;
  created_at: string;
  expires_at: string | null;
  last_used_at?: string;
}

export interface CreatedAccessToken {
  token: AccessToken;
  secret: string;
}

export interface FileVersion {
  id: string;
  version: number;
  filename: string;
  content_type: string;
  size: number;
  sha256: string;
  created_at: string;
  created_by: string;
  is_current: boolean;
  availability: 'available' | 'pending' | 'reclaimed';
}

export interface DocumentUpload {
  item: Resource;
  file: { id: string; filename: string; size: number; content_type: string };
  duplicates?: { item_id: string; name: string }[] | null;
}

export interface Scope {
  name: string;
  granted_to_members: boolean;
}

/** An answer a task offers; each is a workflow port. */
export interface TaskOutcome {
  key: string;
  label: string;
  style?: 'primary' | 'danger' | 'default';
  comment?: 'none' | 'optional' | 'required';
  skip_form?: boolean;
  allow_stale?: boolean;
  decisive?: boolean;
}

export interface TaskAnswer {
  subject: string;
  /** The entry answered for: the subject, or group:<id>. */
  assignee: string;
  outcome: string;
  comment?: string;
  input?: Record<string, unknown>;
  created_at: string;
}

/** One message to one person: their inbox. */
export interface Notification {
  id: string;
  type: 'message' | 'task.assigned' | 'task.completed' | 'workflow.failed' | 'comment.mentioned' | 'comment.replied';
  title: string;
  body?: string;
  workspace_id?: string;
  /** What the notification opens: at most one of these. */
  item_id?: string;
  task_id?: string;
  run_id?: string;
  /** The comment whose thread it opens on item_id. */
  comment_id?: string;
  /** How many notifications it stands for, such as replies while it was unread. */
  count: number;
  source: 'workflow' | 'user' | 'system';
  /** Who sent it or caused what it reports. */
  sender?: string;
  workflow_id?: string;
  created_at: string;
  read_at?: string;
}

/** Who takes part in a collection's discussions. */
export type CommentMode = 'off' | 'collaborators' | 'readers';

export interface CommentPerson {
  subject: string;
  name: string;
}

/** A comment on an item; a thread's first comment has thread_id equal to id. */
export interface Comment {
  id: string;
  item_id: string;
  thread_id: string;
  author: CommentPerson;
  source: 'user' | 'workflow';
  workflow_id?: string;
  /** Plain text; <@user:subject> mentions a person. Empty once deleted. */
  body: string;
  mentions: CommentPerson[];
  created_at: string;
  updated_at: string;
  edited_at?: string;
  deleted?: boolean;
  resolved_at?: string;
  resolved_by?: CommentPerson;
  version: number;
  can_edit: boolean;
  can_delete: boolean;
  can_resolve?: boolean;
  /** On a thread's first comment: where in the item it is, such as a file.region on a page. */
  anchor?: { kind: string; [key: string]: unknown };
}

export interface CommentThread extends Comment {
  replies: Comment[];
}

/** An item's threads and what the reader may do in its discussion. */
export interface CommentPage extends Page<CommentThread> {
  mode: CommentMode;
  mode_from: 'deployment' | 'organization' | 'workspace' | 'collection' | 'default';
  can_read: boolean;
  can_post: boolean;
}

export interface MentionCandidate extends CommentPerson {
  can_see: boolean;
}

export interface CommentSettings {
  mode: CommentMode | null;
  effective: { mode: CommentMode; from: CommentPage['mode_from'] };
  version: number;
}

export interface OrganizationCommentSettings {
  enabled: boolean;
  mode: CommentMode | null;
  available: boolean;
  version: number;
}

/** An action request: an approval, or any task a workflow, person or the system created. */
export interface Task {
  id: string;
  workspace_id: string;
  item_id?: string;
  kind: 'task' | 'approval';
  key?: string;
  title: string;
  description?: string;
  assignees: string[];
  completion: 'any' | 'all';
  outcomes: TaskOutcome[];
  input_schema?: FormSchema;
  values?: Record<string, unknown>;
  review?: { type: 'file.compare' | 'file.preview'; staged_file_id: string };
  priority: 'low' | 'normal' | 'high';
  due_at?: string;
  source: 'workflow' | 'user' | 'system';
  workflow_id?: string;
  run_id?: string;
  /** The operation a workflow task's answer ends, which its run waits on. */
  operation_id?: string;
  status: 'open' | 'completed' | 'cancelled' | 'expired';
  outcome?: string;
  completed_by?: string;
  completed_at?: string;
  comment?: string;
  input?: Record<string, unknown>;
  created_by: string;
  created_at: string;
  updated_at: string;
  version: number;
  responses?: TaskAnswer[];
  answered?: boolean;
  can_respond: boolean;
  /** Set when one task is read: the caller may change its assignees. */
  can_reassign?: boolean;
}

export interface StagedFile {
  id: string;
  item_id: string;
  artifact_id: string;
  /** The workflow run that staged it; absent when a person or client did. */
  run_id?: string;
  filename: string;
  content_type: string;
  size: number;
  sha256: string;
  metrics: Record<string, unknown>;
  sources: { item_id: string; blob_id: string }[];
  state: 'pending' | 'promoted' | 'discarded';
  fresh: boolean;
  created_at: string;
}
