import { File, FileArchive, FileAudio, FileCode, FileImage, FileSpreadsheet, FileText, FileVideo, Presentation, type LucideIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

export type FileFamily = 'pdf' | 'image' | 'text' | 'document' | 'spreadsheet' | 'presentation' | 'archive' | 'audio' | 'video' | 'code' | 'file';

const labels: Record<string, string> = {
  'application/pdf': 'PDF document',
  'image/tiff': 'TIFF image',
  'image/jpeg': 'JPEG image',
  'image/png': 'PNG image',
  'image/webp': 'WebP image',
  'image/gif': 'GIF image',
  'text/markdown': 'Markdown document',
  'text/plain': 'Text file',
  'text/csv': 'CSV file',
  'application/json': 'JSON file',
  'application/msword': 'Word document',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document': 'Word document',
  'application/vnd.oasis.opendocument.text': 'OpenDocument text',
  'application/rtf': 'Rich text document',
  'application/vnd.ms-excel': 'Excel workbook',
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': 'Excel workbook',
  'application/vnd.oasis.opendocument.spreadsheet': 'OpenDocument spreadsheet',
  'application/vnd.ms-powerpoint': 'PowerPoint presentation',
  'application/vnd.openxmlformats-officedocument.presentationml.presentation': 'PowerPoint presentation',
  'application/vnd.oasis.opendocument.presentation': 'OpenDocument presentation',
  'application/zip': 'ZIP archive',
  'message/rfc822': 'Email message',
};

const families: [FileFamily, (media: string) => boolean][] = [
  ['pdf', (media) => media === 'application/pdf'],
  ['image', (media) => media.startsWith('image/')],
  ['spreadsheet', (media) => media === 'text/csv' || /spreadsheet|ms-excel/.test(media)],
  ['presentation', (media) => /presentation|ms-powerpoint/.test(media)],
  ['document', (media) => /wordprocessing|msword|opendocument\.text|rtf/.test(media) || media === 'message/rfc822'],
  ['code', (media) => media === 'application/json' || media === 'application/xml' || media === 'text/html'],
  ['text', (media) => media.startsWith('text/')],
  ['archive', (media) => /zip|x-7z|x-tar|gzip|x-rar/.test(media)],
  ['audio', (media) => media.startsWith('audio/')],
  ['video', (media) => media.startsWith('video/')],
];

export function fileFamily(media: string | undefined): FileFamily {
  return (media && families.find(([, matches]) => matches(media))?.[0]) || 'file';
}

/** A person's name for a media type: "PDF document", or the type itself when it has none. */
export function fileTypeLabel(media: string | undefined): string {
  if (!media) return 'File';
  return labels[media] ?? `${media.split('/')[1]?.replace(/^(x-|vnd\.)/, '').toUpperCase() || 'Unknown'} file`;
}

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
  code: [FileCode, 'text-muted'],
  file: [File, 'text-muted'],
};

/** The icon of a file's family, coloured so kinds are told apart at a glance. */
export function FileTypeIcon({ media, className }: { media: string | undefined; className?: string }) {
  const [Icon, tone] = icons[fileFamily(media)];
  return <Icon aria-hidden className={cn('size-4 shrink-0', tone, className)} />;
}
