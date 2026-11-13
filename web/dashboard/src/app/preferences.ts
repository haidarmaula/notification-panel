import { createEffect, onCleanup } from 'solid-js';
import { createStore } from 'solid-js/store';

type Theme = 'light' | 'dark' | 'system';
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
    /* Preferences still work for this visit. */
  }
}
const saved = read('hello.theme');
export const [preferences, setPreferences] = createStore({
  theme: (saved === 'light' || saved === 'dark' ? saved : 'system') as Theme,
  sidebarHidden: read('hello.sidebar') === 'hidden',
});
export function setTheme(value: Theme) {
  setPreferences('theme', value);
  persist('hello.theme', value);
}
export function setSidebarHidden(value: boolean) {
  setPreferences('sidebarHidden', value);
  persist('hello.sidebar', value ? 'hidden' : 'visible');
}
export function initAppearance() {
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const apply = () => {
    document.documentElement.dataset.theme =
      preferences.theme === 'system' ? (media.matches ? 'dark' : 'light') : preferences.theme;
  };
  createEffect(apply);
  media.addEventListener('change', apply);
  onCleanup(() => media.removeEventListener('change', apply));
  apply();
}
