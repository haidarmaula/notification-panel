import { AndroidNotificationPreview } from '../campaigns/AndroidNotificationPreview';
import { CreateAction, SubmitAction } from '../../components/ui/Actions';
import { CheckboxInput } from '@astryxdesign/core/CheckboxInput';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Grid } from '@astryxdesign/core/Grid';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Link } from '@astryxdesign/core/Link';
import { Section } from '@astryxdesign/core/Section';
import { Stack } from '@astryxdesign/core/Stack';
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
} from '@astryxdesign/core/Table';
import { TextArea } from '@astryxdesign/core/TextArea';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { data } from '../../lib/api/client';
import type { Application } from '../../lib/api/types';
import type { components } from '../../lib/api/generated/staff';
import { hasPageNavigation, usePage, useWrite } from './data-hooks';
import { Pager, QueryState, WriteFeedback } from './shared';
import { useUnsaved } from '../../app/unsaved';
import { PageFrame } from '../../components/ui/PageFrame';
import { Status } from '../../components/ui/Feedback';
type Template = components['schemas']['Template'];
const byteLength = (value: string) => new TextEncoder().encode(value).length;
// Truncate on code point boundaries so a multibyte character is never split.
function truncateToBytes(value: string, maxBytes: number) {
  if (byteLength(value) <= maxBytes) return value;
  let result = '';
  let used = 0;
  for (const character of value) {
    const size = byteLength(character);
    if (used + size > maxBytes) break;
    result += character;
    used += size;
  }
  return result;
}
export default function Templates() {
  const { appId = '' } = useParams();
  const page = usePage<Template>(`/apps/${appId}/templates`);
  return (
    <PageFrame
      footer={hasPageNavigation(page) ? <Pager page={page} /> : undefined}
      title="Templates"
      description="Save frequently used messages for future campaigns."
      actions={<CreateAction label="Create template" href={`/apps/${appId}/templates/new`} />}
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label="Message templates">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>Template</TableHeaderCell>
                  <TableHeaderCell>Message title</TableHeaderCell>
                  <TableHeaderCell>Status</TableHeaderCell>
                  <TableHeaderCell>Action</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Link href={`/apps/${appId}/templates/${item.id}`}>{item.name}</Link>{' '}
                      <Text type="supporting" color="secondary">
                        #{item.id}
                      </Text>
                    </TableCell>
                    <TableCell>{item.title_template}</TableCell>
                    <TableCell>
                      <Status value={item.is_active ? 'ACTIVE' : 'INACTIVE'} />
                    </TableCell>
                    <TableCell>
                      {item.is_active && (
                        <Link href={`/apps/${appId}/campaigns/new?template=${item.id}`}>Use</Link>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No templates yet"
            description="Create a template to reuse message content."
          />
        )}
      </QueryState>
    </PageFrame>
  );
}
export function TemplateEditor() {
  const { appId = '', templateId } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const application = useQuery({
    queryKey: ['application', appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${appId}`, signal),
    enabled: Boolean(appId),
  });
  const [name, setName] = useState('');
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [active, setActive] = useState(true);
  const nameBytes = useMemo(() => byteLength(name), [name]);
  const titleBytes = useMemo(() => byteLength(title), [title]);
  const bodyBytes = useMemo(() => byteLength(body), [body]);
  const loaded = useRef(false);
  const query = useQuery({
    queryKey: ['template', appId, templateId],
    queryFn: ({ signal }) => data<Template>(`/apps/${appId}/templates/${templateId}`, signal),
    enabled: !!templateId,
  });
  const formQuery = {
    isPending: !!templateId && query.isPending,
    isError: !!templateId && query.isError,
    error: query.error,
    refetch: query.refetch,
  };
  useEffect(() => {
    if (query.data && !loaded.current) {
      loaded.current = true;
      setName(query.data.name);
      setTitle(query.data.title_template);
      setBody(query.data.body_template);
      setActive(query.data.is_active);
    }
  }, [query.data]);
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const destination = `/apps/${appId}/templates`;
    const origin = window.location.pathname;
    const result = await operation.write<{ data: Template }>(
      `${destination}${templateId ? `/${templateId}` : ''}`,
      templateId ? 'PUT' : 'POST',
      { name: name.trim(), title_template: title, body_template: body, is_active: active },
    );
    if (result) {
      setDirty(false);
      client.setQueryData(['template', appId, result.data.id], result.data);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (window.location.pathname === origin) navigate(destination);
    }
  }
  return (
    <PageFrame
      title={templateId ? 'Edit template' : 'Create template'}
      back={{ label: 'Back to templates', href: `/apps/${appId}/templates` }}
      contentWidth={1120}
    >
      <QueryState query={formQuery} loadingVariant="form">
        <form onInput={() => setDirty(true)} onSubmit={(event) => void save(event)}>
          <Grid columns={{ minWidth: 420, max: 2 }} columnGap={8} rowGap={6} align="start">
            <FormLayout>
              <TextInput
                label="Template name"
                isRequired
                value={name}
                onChange={(value) => setName(truncateToBytes(value, 255))}
                description={`${nameBytes}/255 bytes`}
                isDisabled={operation.blocked()}
              />
              <TextInput
                label="Notification title"
                isRequired
                value={title}
                onChange={(value) => setTitle(truncateToBytes(value, 255))}
                description={`${titleBytes}/255 bytes`}
                isDisabled={operation.blocked()}
              />
              <TextArea
                label="Message"
                isRequired
                rows={6}
                value={body}
                onChange={(value) => setBody(truncateToBytes(value, 4096))}
                description={`${bodyBytes}/4096 bytes`}
                isDisabled={operation.blocked()}
              />
              <CheckboxInput
                label="Active for new campaigns"
                value={active}
                onChange={setActive}
                isDisabled={operation.blocked()}
              />
              <WriteFeedback operation={operation} />
              <Stack direction="horizontal" gap={2} wrap="wrap">
                <SubmitAction
                  label={operation.pending() ? 'Saving…' : 'Save template'}

                  isLoading={operation.pending()}
                  isDisabled={operation.blocked()}
                />
              </Stack>
            </FormLayout>
            <Stack gap={4}>
              <Heading level={2}>Notification preview</Heading>
              <AndroidNotificationPreview
                applicationName={application.data?.name ?? 'Application'}
                title={title}
                body={body}
                context="template"
              />
            </Stack>
          </Grid>
        </form>
      </QueryState>
    </PageFrame>
  );
}
