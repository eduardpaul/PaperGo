const key = 'papergo.recentWorkspace';

/** Remembers the workspace a person last opened, so search can start there. Storage may be unavailable. */
export function rememberWorkspace(id: string) {
  try {
    localStorage.setItem(key, id);
  } catch {
    // Private windows and blocked storage: search starts from its other defaults.
  }
}

export function recentWorkspace(): string | undefined {
  try {
    return localStorage.getItem(key) ?? undefined;
  } catch {
    return undefined;
  }
}
