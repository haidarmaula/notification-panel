import { createContext, useContext, useState } from 'react';
import { themes, defaultThemeSlug, type ThemeSlug } from '../astryx-themes';
export type Appearance = 'light' | 'dark' | 'system';
export const themeLabels: Record<ThemeSlug, string> = {
  stone: 'Stone',
  y2k: 'Y2K',
  matcha: 'Matcha',
};
function read(key: string) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function persist(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* Preferences remain available for this visit. */
  }
}
export function isThemeSlug(value: string): value is ThemeSlug {
  return Object.hasOwn(themes, value);
}
export function usePreferences() {
  const [appearance, updateAppearance] = useState<Appearance>(() => {
    const value = read('hello.theme');
    return value === 'light' || value === 'dark' ? value : 'system';
  });
  const [theme, updateTheme] = useState<ThemeSlug>(() => {
    const value = read('hello.design-theme');
    return value && isThemeSlug(value) ? value : defaultThemeSlug;
  });
  const [sidebarCollapsed, updateSidebar] = useState(() => read('hello.sidebar') === 'hidden');
  return {
    appearance,
    theme,
    sidebarCollapsed,
    setAppearance(value: Appearance) {
      updateAppearance(value);
      persist('hello.theme', value);
    },
    setTheme(value: ThemeSlug) {
      updateTheme(value);
      persist('hello.design-theme', value);
    },
    setSidebarCollapsed(value: boolean) {
      updateSidebar(value);
      persist('hello.sidebar', value ? 'hidden' : 'visible');
    },
  };
}
export const AppearanceContext = createContext<ReturnType<typeof usePreferences> | undefined>(
  undefined,
);
export function useAppearance() {
  const value = useContext(AppearanceContext);
  if (!value) throw new Error('Missing appearance provider');
  return value;
}

export function resolveTheme(slug: ThemeSlug) {
  return themes[slug];
}
