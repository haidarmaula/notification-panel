import { SubmitAction } from '../../components/ui/Actions';
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
  const replacement = useRef<HTMLInputElement>(null);
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    if (new TextEncoder().encode(password).length > 72 || password !== confirmation) {
      setError(
        password !== confirmation
          ? 'The password confirmation does not match.'
          : 'The new password can be at most 72 bytes.',
      );
      replacement.current?.focus();
      return;
    }
    if (
      !window.confirm(
        'Change the password and sign out all account sessions? Any unsaved changes will be lost.',
      )
    )
      return;
    const result = await operation.write('/profile/password', 'PATCH', {
      current_password: current,
      new_password: password,
    });
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
        <form onInput={() => setDirty(true)} onSubmit={(event) => void save(event)}>
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
              onChange={setPassword}
              isDisabled={operation.blocked()}
              status={error ? { type: 'error', message: error } : undefined}
            />
            <TextInput
              label="Confirm new password"
              type="password"
              autoComplete="new-password"
              isRequired
              value={confirmation}
              onChange={setConfirmation}
              isDisabled={operation.blocked()}
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
    </Section>
  );
}
