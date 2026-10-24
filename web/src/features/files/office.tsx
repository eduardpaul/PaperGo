import { useQuery } from '@tanstack/react-query';
import { ExternalLink } from 'lucide-react';
import { api } from '@/api/http';
import type { Resource } from '@/api/types';
import { buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { FileItem } from './file-action';

/** The Office app that edits a type, and its URI scheme. */
interface OfficeApp {
  name: 'Word' | 'Excel' | 'PowerPoint';
  scheme: 'ms-word' | 'ms-excel' | 'ms-powerpoint';
}

const word: OfficeApp = { name: 'Word', scheme: 'ms-word' };
const excel: OfficeApp = { name: 'Excel', scheme: 'ms-excel' };
const powerpoint: OfficeApp = { name: 'PowerPoint', scheme: 'ms-powerpoint' };

const apps: Record<string, OfficeApp> = {
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document': word,
  'application/msword': word,
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': excel,
  'application/vnd.ms-excel': excel,
  'application/vnd.openxmlformats-officedocument.presentationml.presentation': powerpoint,
  'application/vnd.ms-powerpoint': powerpoint,
};

/** The Office app for a media type, if one edits it. */
export function officeApp(contentType: string | undefined): OfficeApp | undefined {
  return contentType ? apps[contentType] : undefined;
}

/** The Office URI that opens a WebDAV address for editing: Office saves back over WebDAV. */
export function officeUri(app: OfficeApp, address: string) {
  return `${app.scheme}:ofe|u|${address}`;
}

/**
 * The item's address in its library's WebDAV share, from the names of the
 * folders above it. Undefined until they load, or when the library is not
 * shared over WebDAV.
 */
export function useWebDAVAddress(item: FileItem & { parent_id?: string }, collection: Pick<Resource, 'id' | 'webdav_enabled'> | undefined) {
  const enabled = !!collection?.webdav_enabled && item.container_id === collection.id;
  const path = useQuery({
    queryKey: ['webdav-path', item.id, item.parent_id, item.name],
    enabled,
    staleTime: 60_000,
    queryFn: async () => {
      const names = [item.name];
      const seen = new Set<string>();
      let parent = item.parent_id;
      while (parent && parent !== collection!.id && !seen.has(parent) && seen.size < 64) {
        seen.add(parent);
        const folder = (await api<Resource>(`/v1/resources/${parent}`)).data;
        names.unshift(folder.name);
        parent = folder.parent_id;
      }
      return names;
    },
  });
  if (!enabled || !path.data) return undefined;
  return `${window.location.origin}/webdav/${collection!.id}/${path.data.map(encodeURIComponent).join('/')}`;
}

/** The link that opens the item's file in its Office app, once its WebDAV address is known. */
export function useOfficeLink(item: FileItem & { parent_id?: string }, collection: Pick<Resource, 'id' | 'webdav_enabled'> | undefined) {
  const app = officeApp(item.file?.content_type);
  const address = useWebDAVAddress(item, app ? collection : undefined);
  return app && address ? { app, href: officeUri(app, address) } : undefined;
}

/**
 * Open in Word (or Excel, or PowerPoint) for an Office file in a library
 * shared over WebDAV: the desktop app opens the file from PaperGo and each
 * save there is a new file version. Nothing shows for other files.
 */
export function OpenInOffice({ item, collection, className, size = 'md' }: { item: FileItem & { parent_id?: string }; collection: Pick<Resource, 'id' | 'webdav_enabled'> | undefined; className?: string; size?: 'sm' | 'md' }) {
  const link = useOfficeLink(item, collection);
  if (!link) return null;
  return (
    <a
      href={link.href}
      title={`Opens in the ${link.app.name} app on this computer; saving there adds a file version here`}
      className={cn(buttonVariants({ size }), className)}
      onClick={(event) => event.stopPropagation()}
    >
      <ExternalLink /> Open in {link.app.name}
    </a>
  );
}

