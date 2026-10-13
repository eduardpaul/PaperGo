import type { FormSchema } from '@/features/workflows/model';

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
  allow_folders?: boolean;
  template_keys?: string[] | null;
  tags?: string[] | null;
  values?: Record<string, unknown> | null;
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

export interface ContentType {
  id: string;
  key: string;
  name: string;
  field_ids?: string[] | null;
  is_default?: boolean;
  version?: number;
  rules?: ValidationRule[];
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
  id: string;
  container_id: string;
  name: string;
  columns?: string[] | null;
  layout?: string;
  is_default?: boolean;
  version: number;
  query?: QuerySpec;
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
  filter?: FilterExpr;
  sort?: { field: string; direction: string }[];
  group_by?: string;
  content_type_id?: string;
  parent_id?: string;
  /** Anywhere below this folder: in it or its subfolders. */
  ancestor_id?: string;
  search?: string;
  tag?: string;
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
  definition?: SmartFolderDefinition;
  version: number;
}

export interface SmartFolderDefinition {
  collections?: string[];
  templates?: string[];
  content_types?: string[];
  terms?: string[];
  term_match?: string;
  filter?: { field?: string; op?: string; value?: unknown };
  group_by?: { field: string; by?: string }[];
  include_folders?: boolean;
}

export interface SmartFolderEntry {
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
  path?: string;
}

export interface Workflow {
  id: string;
  workspace_id: string;
  key: string;
  name: string;
  description: string;
  enabled: boolean;
  builtin_key?: string;
  collection_id?: string;
  parameters?: Record<string, unknown>;
  current_version: number;
  definition: unknown;
  version: number;
}

export interface BuiltInState {
  key: string;
  name: string;
  description: string;
  per_collection?: boolean;
  parameters_schema?: { properties?: Record<string, { title?: string; description?: string; default?: unknown }> };
  workflows?: Workflow[] | null;
}

export interface WorkflowRun {
  id: string;
  workflow_id: string;
  status: string;
  event_type?: string;
  item_id?: string;
  error?: string;
  created_at: string;
  actor?: string;
}

export interface RestToken {
  id: string;
  name: string;
  prefix: string;
  scopes?: string[] | null;
  created_at: string;
  expires_at: string;
  last_used_at?: string;
}

export interface CreatedRestToken {
  token: RestToken;
  secret: string;
}

export interface WebDAVCredential {
  id: string;
  label: string;
  created_at: string;
  expires_at?: string;
  password?: string;
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

export interface Approval {
  id: string;
  workspace_id: string;
  workflow_id: string;
  run_id: string;
  node: string;
  item_id?: string;
  title: string;
  description?: string;
  assignees: string[];
  input_schema?: FormSchema;
  review?: { type: 'file.compare' | 'file.preview'; staged_file_id: string };
  status: 'open' | 'approved' | 'rejected' | 'cancelled';
  decided_by?: string;
  decided_at?: string;
  comment?: string;
  input?: Record<string, unknown>;
  created_at: string;
  created_by: string;
}

export interface StagedFile {
  id: string;
  item_id: string;
  run_id: string;
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
