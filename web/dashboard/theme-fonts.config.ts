import { fileURLToPath } from 'node:url';

// Serve theme fonts locally without weakening the dashboard Content Security Policy.
export const themeFontAliases = [
  'neutral',
  'stone',
  'y2k',
  'matcha',
  'butter',
  'chocolate',
  'gothic',
].map((theme) => ({
  find: `@astryxdesign/theme-${theme}/fonts.css`,
  replacement: fileURLToPath(new URL(`./fonts/${theme}.css`, import.meta.url)),
}));
