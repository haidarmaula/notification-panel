import { useCallback, useEffect, useRef, useState } from 'react';
import { useBlocker } from 'react-router-dom';

const forms = new Map<symbol, { isDirty: () => boolean; clear: () => void }>();

let pending: ((value: boolean) => void) | null = null;
const listeners = new Set<() => void>();
function emit() {
  for (const listener of listeners) listener();
}
export function subscribeUnsavedConfirm(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
export function unsavedConfirmPending() {
  return pending !== null;
}
export function resolveUnsavedConfirm(value: boolean) {
  const resolve = pending;
  pending = null;
  emit();
  resolve?.(value);
}
function requestUnsavedConfirm(): Promise<boolean> {
  if (pending) return Promise.resolve(false);
  return new Promise((resolve) => {
    pending = resolve;
    emit();
  });
}
export async function confirmUnsavedChanges() {
  if (!Array.from(forms.values()).some((form) => form.isDirty())) return true;
  return requestUnsavedConfirm();
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
    void requestUnsavedConfirm().then((leave) => {
      if (leave) blocker.proceed();
      else blocker.reset();
    });
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
