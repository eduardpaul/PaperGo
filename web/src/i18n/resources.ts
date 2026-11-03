// The English catalogs, bundled with the app. They are the source of every key and the type of t().
// A new namespace is a JSON file in each src/locales/<code> folder and a line here.

import auth from '@/locales/en/auth.json';
import common from '@/locales/en/common.json';
import errors from '@/locales/en/errors.json';
import settings from '@/locales/en/settings.json';
import shell from '@/locales/en/shell.json';

export const en = { auth, common, errors, settings, shell };

export type Namespace = keyof typeof en;
export const namespaces = Object.keys(en) as Namespace[];
