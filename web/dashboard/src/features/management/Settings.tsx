import { SubmitAction } from '../../components/ui/Actions';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Section } from '@astryxdesign/core/Section';
import { Selector } from '@astryxdesign/core/Selector';
import { Stack } from '@astryxdesign/core/Stack';
import { TextInput } from '@astryxdesign/core/TextInput';
import { MetadataList, MetadataListItem } from '@astryxdesign/core/MetadataList';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { data } from '../../lib/api/client';
import type { Application } from '../../lib/api/types';
import { useAppearance, themeLabels } from '../../app/appearance-context';
import { useWrite } from './data-hooks';
import { QueryState, WriteFeedback } from './shared';
import { useUnsaved } from '../../app/unsaved';
import { PageFrame } from '../../components/ui/PageFrame';
import { Status } from '../../components/ui/Feedback';
import PasswordForm from './PasswordForm';
export default function Settings() {
  const { appId } = useParams();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const preferences = useAppearance();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [saved, setSaved] = useState(false);
  const loaded = useRef(false);
  const profile = useQuery({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ name: string; email: string }>('/profile', signal),
  });
  const app = useQuery({
    queryKey: ['application', appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${appId}`, signal),
    enabled: !!appId,
  });
  useEffect(() => {
    if (profile.data && !loaded.current) {
      loaded.current = true;
      setName(profile.data.name);
      setEmail(profile.data.email);
    }
  }, [profile.data]);
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaved(false);
    const result = await operation.write('/profile', 'PATCH', {
      name: name.trim(),
      email: email.trim(),
    });
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['profile'] });
      setSaved(true);
    }
  }
  return (
    <PageFrame
      title="My account"
      description="Manage display preferences, your profile, and account security."
      contentWidth={720}
    >
      <Section padding={6}>
        <Stack gap={4}>
          <Heading level={2}>Appearance</Heading>
          <Selector
            label="Appearance"
            options={[
              { value: 'system', label: 'System' },
              { value: 'light', label: 'Light' },
              { value: 'dark', label: 'Dark' },
            ]}
            value={preferences.appearance}
            onChange={(value) =>
              value && preferences.setAppearance(value as 'system' | 'light' | 'dark')
            }
          />
          <Selector
            label="Design theme"
            options={Object.entries(themeLabels).map(([value, label]) => ({ value, label }))}
            value={preferences.theme}
            onChange={(value) => value && preferences.setTheme(value as keyof typeof themeLabels)}
          />
          <Text>
            Appearance, theme, and sidebar preferences are stored in this browser. Account and
            campaign data are not stored there.
          </Text>
        </Stack>
      </Section>
      {appId && (
        <Section padding={6}>
          <Stack gap={4}>
            <Heading level={2}>Application and environment</Heading>
            <QueryState query={app}>
              {app.data && (
                <MetadataList label={{ position: 'start' }}>
                  <MetadataListItem label="Name">{app.data.name}</MetadataListItem>
                  <MetadataListItem label="Application key">{app.data.key}</MetadataListItem>
                  <MetadataListItem label="Application ID">{app.data.id}</MetadataListItem>
                  <MetadataListItem label="Status">
                    <Status value={app.data.status} />
                  </MetadataListItem>
                  <MetadataListItem label="Dashboard address">
                    {window.location.origin}
                  </MetadataListItem>
                </MetadataList>
              )}
            </QueryState>
            <Text>
              Provider configuration and delivery policies are managed through protected operational
              procedures.
            </Text>
          </Stack>
        </Section>
      )}
      <Section padding={6}>
        <Stack gap={4}>
          <Heading level={2}>My profile</Heading>
          <QueryState query={profile} loadingVariant="form">
            <form onInput={() => setDirty(true)} onSubmit={(event) => void save(event)}>
              <FormLayout>
                <TextInput
                  label="Name"
                  isRequired
                  value={name}
                  onChange={(value) => {
                    setName(value.slice(0, 255));
                    setSaved(false);
                  }}
                  isDisabled={operation.blocked()}
                />
                <TextInput
                  label="Email"
                  type="email"
                  isRequired
                  value={email}
                  onChange={(value) => {
                    setEmail(value);
                    setSaved(false);
                  }}
                  isDisabled={operation.blocked()}
                />
                <WriteFeedback operation={operation} />
                {saved && <Text role="status">Profile updated successfully.</Text>}
                <Stack direction="horizontal">
                  <SubmitAction
                    label={operation.pending() ? 'Saving…' : 'Save profile'}

                    isLoading={operation.pending()}
                    isDisabled={operation.blocked()}
                  />
                </Stack>
              </FormLayout>
            </form>
          </QueryState>
        </Stack>
      </Section>
      <PasswordForm />
    </PageFrame>
  );
}
