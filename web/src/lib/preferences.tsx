import { useQuery } from '@tanstack/react-query';
import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { preferencesQuery } from '@/api/queries';
import { createFormatter, type Formatter } from './format';

export type Theme = 'system' | 'light' | 'dark';
const ThemeKey = 'papergo.theme';

const FormatterContext = createContext<Formatter>(createFormatter());

export function useFormat(): Formatter {
  return useContext(FormatterContext);
}

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const { data } = useQuery(preferencesQuery);
  const formatter = useMemo(
    () =>
      createFormatter({
        language: data?.language,
        timeZone: data?.time_zone,
        dateFormat: data?.date_format,
        timeFormat: data?.time_format === '12h' ? '12h' : '24h',
        numberFormat: data?.number_format,
      }),
    [data],
  );

  useEffect(() => {
    if (data?.theme) applyTheme(data.theme);
  }, [data?.theme]);

  useEffect(() => {
    if (data?.language) document.documentElement.lang = data.language;
  }, [data?.language]);

  return <FormatterContext.Provider value={formatter}>{children}</FormatterContext.Provider>;
}

export function applyTheme(theme: Theme) {
  try {
    localStorage.setItem(ThemeKey, theme);
  } catch {
    // Storage can be unavailable; the theme still applies to this page.
  }
  const dark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', dark);
}

export function storedTheme(): Theme {
  try {
    const value = localStorage.getItem(ThemeKey);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch {
    return 'system';
  }
}
