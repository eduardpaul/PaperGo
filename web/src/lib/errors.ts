import { ApiError } from '@/api/http';
import { i18n } from '@/i18n';

/** A problem in the user's language when its code has a fixed meaning; otherwise the server's own detail. */
export function problemMessage(error: unknown): string {
  if (error instanceof ApiError) {
    const key = `errors:code.${error.code}`;
    return i18n.exists(key) ? i18n.t(key as 'errors:generic') : error.message;
  }
  if (error instanceof Error) return error.message;
  return i18n.t('errors:generic');
}
