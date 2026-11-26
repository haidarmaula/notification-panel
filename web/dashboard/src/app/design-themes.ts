import { defineTheme } from '@astryxdesign/core/theme';
import { themes as baseThemes, defaultThemeSlug } from '../astryx-themes';

// Secondary text is darkened/lightened per mode so supporting copy reaches
// 7:1 on both body and card surfaces (AAA body text) in every production theme.
export const themes = {
  stone: defineTheme({
    name: 'stone',
    extends: baseThemes.stone,
    tokens: { '--color-text-secondary': ['#47474d', '#b8b8c0'] },
  }),
  y2k: defineTheme({
    name: 'y2k',
    extends: baseThemes.y2k,
    tokens: { '--color-text-secondary': ['#3f382e', '#a6acd6'] },
  }),
  matcha: defineTheme({
    name: 'matcha',
    extends: baseThemes.matcha,
    tokens: { '--color-text-secondary': ['#43511f', '#a8b87c'] },
  }),
} as const;
export type ThemeSlug = keyof typeof themes;
export { defaultThemeSlug };
