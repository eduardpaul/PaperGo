import { File, FileArchive, FileAudio, FileCode, FileImage, FileSpreadsheet, FileText, FileVideo, Mail, Presentation, type LucideIcon } from 'lucide-react';
import { fileTypes } from '@/api/auth';
import { cn } from '@/lib/utils';

export type FileFamily = 'pdf' | 'image' | 'text' | 'document' | 'spreadsheet' | 'presentation' | 'archive' | 'audio' | 'video' | 'email' | 'code' | 'file';

const icons: Record<FileFamily, [LucideIcon, string]> = {
  pdf: [FileText, 'text-red-600 dark:text-red-400'],
  image: [FileImage, 'text-violet-600 dark:text-violet-400'],
  text: [FileText, 'text-muted'],
  document: [FileText, 'text-blue-600 dark:text-blue-400'],
  spreadsheet: [FileSpreadsheet, 'text-green-600 dark:text-green-400'],
  presentation: [Presentation, 'text-orange-600 dark:text-orange-400'],
  archive: [FileArchive, 'text-amber-600 dark:text-amber-400'],
  audio: [FileAudio, 'text-pink-600 dark:text-pink-400'],
  video: [FileVideo, 'text-pink-600 dark:text-pink-400'],
  email: [Mail, 'text-sky-600 dark:text-sky-400'],
  code: [FileCode, 'text-muted'],
  file: [File, 'text-muted'],
};

const known = (media: string | undefined) => (media ? fileTypes().find((type) => type.media_type === media) : undefined);

/** The icon family of a media type: the server's, else one guessed from its top-level type. */
export function fileFamily(media: string | undefined): FileFamily {
  const family = known(media)?.family;
  if (family && family in icons) return family as FileFamily;
  const top = media?.split('/')[0];
  return top === 'image' || top === 'audio' || top === 'video' || top === 'text' ? top : 'file';
}

/** A person's name for a media type: the server's, such as "PDF document", else one made from the type. */
export function fileTypeLabel(media: string | undefined): string {
  if (!media || media === 'application/octet-stream') return 'File';
  return known(media)?.label ?? `${media.split('/')[1]?.replace(/^(x-|vnd\.)/, '').toUpperCase() || 'Unknown'} file`;
}

/** The icon of a file's family, coloured so kinds are told apart at a glance. */
export function FileTypeIcon({ media, className }: { media: string | undefined; className?: string }) {
  const [Icon, tone] = icons[fileFamily(media)];
  return <Icon aria-hidden className={cn('size-4 shrink-0', tone, className)} />;
}
