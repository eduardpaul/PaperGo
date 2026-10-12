// Applies the last theme before the first paint; the user's preference takes over after sign-in.
try {
  const theme = localStorage.getItem('papergo.theme') ?? 'system';
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', dark);
} catch {
  // Storage can be unavailable (private windows).
}
