// An app installed on a phone starts at its own page, in a window without
// browser controls. That window shows the app alone, without PaperGo's menu
// and top bar, for as long as it stays open.

const Key = 'papergo.installedApp';
const appPage = /^\/w\/[^/]+\/a\/([^/]+)\/?$/;

/** Whether this window is an installed app's rather than a browser tab. */
export function standalone(): boolean {
  return window.matchMedia?.('(display-mode: standalone)').matches === true || (navigator as { standalone?: boolean }).standalone === true;
}

/**
 * Notes, once per window, the app the window was opened for. Later loads in
 * the same window, such as the return from signing in, keep the first answer.
 */
export function rememberInstalledApp(): void {
  try {
    if (sessionStorage.getItem(Key) !== null) return;
    const id = appPage.exec(window.location.pathname)?.[1];
    sessionStorage.setItem(Key, standalone() && id ? id : '');
  } catch {
    // Without session storage the window shows PaperGo around the app.
  }
}

/** The app this window was installed for, or null in PaperGo's own windows. */
export function installedApp(): string | null {
  try {
    return sessionStorage.getItem(Key) || null;
  } catch {
    return null;
  }
}
