import { createSignal, onCleanup, onMount, type Accessor, type Setter } from 'solid-js';
import { useBeforeLeave } from '@solidjs/router';

const forms = new Map<Accessor<boolean>, Setter<boolean>>();
const message = 'Perubahan belum disimpan. Tinggalkan halaman?';

export function confirmUnsavedChanges() {
  return !Array.from(forms.keys()).some((dirty) => dirty()) || window.confirm(message);
}

export function discardUnsavedChanges() {
  for (const setDirty of forms.values()) setDirty(false);
}

export function useUnsaved() {
  const [dirty, setDirty] = createSignal(false);
  onMount(() => {
    forms.set(dirty, setDirty);
  });
  useBeforeLeave((event) => {
    if (dirty() && !window.confirm(message)) event.preventDefault();
  });
  const unload = (event: BeforeUnloadEvent) => {
    if (dirty()) {
      event.preventDefault();
      event.returnValue = '';
    }
  };
  window.addEventListener('beforeunload', unload);
  onCleanup(() => {
    forms.delete(dirty);
    window.removeEventListener('beforeunload', unload);
  });
  return setDirty;
}
