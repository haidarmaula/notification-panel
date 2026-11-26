import { useEffect } from 'react';

export function usePageFocus() {
  useEffect(() => {
    // Only recover focus that fell to the document after a route change; never
    // steal it from an open dialog, drawer, or a control the user already holds.
    const active = document.activeElement;
    if (active && active !== document.body && active !== document.documentElement) return;
    const heading = document.querySelector<HTMLElement>('h1, [role="heading"][aria-level="1"], h2');
    if (!heading) return;
    heading.setAttribute('tabindex', '-1');
    heading.focus({ preventScroll: true });
  }, []);
}
