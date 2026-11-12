import { cn } from '@/lib/utils';

/** What an app's icon is drawn from: its name unless it chose a glyph, on its color or one its name chooses. */
export interface AppLook {
  name: string;
  icon: string;
  color: string;
}

/** How an address under /pwa/ names an app's look. */
export function lookQuery(app: AppLook): URLSearchParams {
  const query = new URLSearchParams({ name: app.name.trim() });
  if (app.icon) query.set('icon', app.icon);
  if (app.color) query.set('color', app.color);
  return query;
}

/** The address of an app's icon; file is one of the sizes the server draws. */
export function iconUrl(app: AppLook, file = 'icon-192.png'): string {
  return `/pwa/${file}?${lookQuery(app)}`;
}

/** An app's icon, as its home-screen icon shows it. */
export function AppIcon({ app, className }: { app: AppLook; className?: string }) {
  return <img src={iconUrl(app)} alt="" className={cn('size-9 shrink-0', className)} />;
}
