import { useCallback, useEffect, useRef, useState } from 'react';
import { useBlocker } from 'react-router-dom';

const forms = new Map<symbol, { isDirty: () => boolean; clear: () => void }>();
const message = 'You have unsaved changes. Leave this page?';
export function confirmUnsavedChanges() {
  return !Array.from(forms.values()).some((form) => form.isDirty()) || window.confirm(message);
}
export function discardUnsavedChanges() {
  for (const form of forms.values()) form.clear();
}
export function useUnsaved() {
  const [dirty, setDirty] = useState(false);
  const currentDirty = useRef(dirty);
  currentDirty.current = dirty;
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      currentDirty.current && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (blocker.state !== 'blocked') return;
    if (window.confirm(message)) blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    const identity = Symbol('form');
    forms.set(identity, {
      isDirty: () => currentDirty.current,
      clear: () => {
        currentDirty.current = false;
        setDirty(false);
      },
    });
    const unload = (event: BeforeUnloadEvent) => {
      if (!currentDirty.current) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', unload);
    return () => {
      forms.delete(identity);
      window.removeEventListener('beforeunload', unload);
    };
  }, []);
  return useCallback((value: boolean) => {
    currentDirty.current = value;
    setDirty(value);
  }, []);
}
