import { SubmitAction } from '../../components/ui/Actions';
import { AlertDialog } from '@astryxdesign/core/AlertDialog';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Section } from '@astryxdesign/core/Section';
import { Stack } from '@astryxdesign/core/Stack';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { discardUnsavedChanges, useUnsaved } from '../../app/unsaved';
import { useWrite } from './data-hooks';
import { WriteFeedback } from './shared';
export default function PasswordForm() {
  const operation = useWrite();
  const client = useQueryClient();
  const setDirty = useUnsaved();
  const [current, setCurrent] = useState('');
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState('');
  const [confirming, setConfirming] = useState(false);
  const replacement = useRef<HTMLInputElement>(null);
  const confirmInput = useRef<HTMLInputElement>(null);
  const mismatch = password !== confirmation;
  function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    if (new TextEncoder().encode(password).length > 72 || mismatch) {
      setError(
        mismatch
          ? 'The password confirmation does not match.'
          : 'The new password can be at most 72 bytes.',
      );
      (mismatch ? confirmInput : replacement).current?.focus();
      return;
    }
    setConfirming(true);
  }
  async function commit() {
    const result = await operation.write('/profile/password', 'PATCH', {
      current_password: current,
      new_password: password,
    });
    setConfirming(false);
    if (result) {
      setCurrent('');
      setPassword('');
      setConfirmation('');
      discardUnsavedChanges();
      client.clear();
      window.location.replace('/login');
    }
  }
  return (
    <Section padding={6}>
      <Stack gap={4}>
        <Heading level={2}>Change password</Heading>
        <Text>
          After a successful change, all account sessions are revoked and you must sign in again.
        </Text>
        <form onInput={() => setDirty(true)} onSubmit={save}>
          <FormLayout>
            <TextInput
              label="Current password"
              type="password"
              autoComplete="current-password"
              isRequired
              value={current}
              onChange={setCurrent}
              isDisabled={operation.blocked()}
            />
            <TextInput
              ref={replacement}
              label="New password"
              type="password"
              autoComplete="new-password"
              isRequired
              value={password}
              onChange={(value) => {
                setPassword(value);
                setError('');
              }}
              isDisabled={operation.blocked()}
              status={error && !mismatch ? { type: 'error', message: error } : undefined}
            />
            <TextInput
              ref={confirmInput}
              label="Confirm new password"
              type="password"
              autoComplete="new-password"
              isRequired
              value={confirmation}
              onChange={(value) => {
                setConfirmation(value);
                setError('');
              }}
              isDisabled={operation.blocked()}
              status={error && mismatch ? { type: 'error', message: error } : undefined}
            />
            <WriteFeedback operation={operation} />
            <Stack direction="horizontal">
              <SubmitAction
                label={operation.pending() ? 'Changing…' : 'Change password'}

                isLoading={operation.pending()}
                isDisabled={operation.blocked()}
              />
            </Stack>
          </FormLayout>
        </form>
      </Stack>
      <AlertDialog
        isOpen={confirming}
        onOpenChange={(open) => {
          if (!open) setConfirming(false);
        }}
        title="Change password and sign out all sessions?"
        description="Every account session is revoked and any unsaved changes will be lost."
        actionLabel="Change password"
        isActionLoading={operation.pending()}
        onAction={() => void commit()}
      />
    </Section>
  );
}