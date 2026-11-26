import { useSyncExternalStore } from 'react';
import { AlertDialog } from '@astryxdesign/core/AlertDialog';
import {
  resolveUnsavedConfirm,
  subscribeUnsavedConfirm,
  unsavedConfirmPending,
} from '../../app/unsaved';

export function UnsavedChangesDialog() {
  const isOpen = useSyncExternalStore(subscribeUnsavedConfirm, unsavedConfirmPending);
  return (
    <AlertDialog
      isOpen={isOpen}
      onOpenChange={(open) => {
        if (!open) resolveUnsavedConfirm(false);
      }}
      title="Leave this page?"
      description="You have unsaved changes. Leaving discards them."
      actionLabel="Leave page"
      cancelLabel="Stay"
      onAction={() => resolveUnsavedConfirm(true)}
    />
  );
}
