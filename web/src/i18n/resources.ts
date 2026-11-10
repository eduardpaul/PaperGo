// The English catalogs, bundled with the app. They are the source of every key and the type of t().
// A new namespace is a JSON file in each src/locales/<code> folder and a line here.

import admin from '@/locales/en/admin.json';
import apps from '@/locales/en/apps.json';
import archive from '@/locales/en/archive.json';
import auth from '@/locales/en/auth.json';
import comments from '@/locales/en/comments.json';
import common from '@/locales/en/common.json';
import documents from '@/locales/en/documents.json';
import errors from '@/locales/en/errors.json';
import fields from '@/locales/en/fields.json';
import files from '@/locales/en/files.json';
import home from '@/locales/en/home.json';
import lists from '@/locales/en/lists.json';
import notifications from '@/locales/en/notifications.json';
import relationships from '@/locales/en/relationships.json';
import search from '@/locales/en/search.json';
import settings from '@/locales/en/settings.json';
import shell from '@/locales/en/shell.json';
import smartFolders from '@/locales/en/smartFolders.json';
import solutions from '@/locales/en/solutions.json';
import tasks from '@/locales/en/tasks.json';
import workflows from '@/locales/en/workflows.json';
import workspaces from '@/locales/en/workspaces.json';

export const en = {
  admin,
  apps,
  archive,
  auth,
  comments,
  common,
  documents,
  errors,
  fields,
  files,
  home,
  lists,
  notifications,
  relationships,
  search,
  settings,
  shell,
  smartFolders,
  solutions,
  tasks,
  workflows,
  workspaces,
};

export type Namespace = keyof typeof en;
export const namespaces = Object.keys(en) as Namespace[];
