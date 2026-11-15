import { useEffect, type PropsWithChildren } from 'react';
import { Theme } from '@astryxdesign/core/theme';
import { AppearanceContext, usePreferences, resolveTheme } from './appearance-context';
export function AppearanceProvider({ children }: PropsWithChildren) {
  const preferences = usePreferences();
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      document.documentElement.dataset.theme =
        preferences.appearance === 'system'
          ? media.matches
            ? 'dark'
            : 'light'
          : preferences.appearance;
    };
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [preferences.appearance]);

  return (
    <AppearanceContext.Provider value={preferences}>
      <Theme theme={resolveTheme(preferences.theme)} mode={preferences.appearance}>
        {children}
      </Theme>
    </AppearanceContext.Provider>
  );
}
