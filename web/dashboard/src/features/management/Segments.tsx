import { CreateAction, SubmitAction } from '../../components/ui/Actions';
import { Button } from '@astryxdesign/core/Button';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Link } from '@astryxdesign/core/Link';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { FormLayout } from '@astryxdesign/core/FormLayout';
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
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { data } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/staff';
import { hasPageNavigation, usePage, useWrite } from './data-hooks';
import { Pager, QueryState, WriteFeedback } from './shared';
import { useUnsaved } from '../../app/unsaved';
import { PageFrame } from '../../components/ui/PageFrame';
type Segment = components['schemas']['Segment'];
type Member = components['schemas']['Member'];
type User = components['schemas']['User'];
export default function Segments() {
  const { appId = '' } = useParams();
  const page = usePage<Segment>(`/apps/${appId}/segments`);
  return (
    <PageFrame
      footer={hasPageNavigation(page) ? <Pager page={page} /> : undefined}
      title="Segments"
      description="Recipient groups whose members you manage manually."
      actions={<CreateAction label="Create segment" href={`/apps/${appId}/segments/new`} />}
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label="Recipient segments">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>Segment</TableHeaderCell>
                  <TableHeaderCell>Description</TableHeaderCell>
                  <TableHeaderCell>Type</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Link href={`/apps/${appId}/segments/${item.id}`}>{item.name}</Link>{' '}
                      <Text type="supporting" color="secondary">
                        #{item.id}
                      </Text>
                    </TableCell>
                    <TableCell>{item.description || '—'}</TableCell>
                    <TableCell>Manual</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No segments yet"
            description="Create a group and add users as members."
          />
        )}
      </QueryState>
    </PageFrame>
  );
}
export function SegmentEditor() {
  const { appId = '', segmentId } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const loaded = useRef(false);
  const query = useQuery({
    queryKey: ['segment', appId, segmentId],
    queryFn: ({ signal }) => data<Segment>(`/apps/${appId}/segments/${segmentId}`, signal),
    enabled: !!segmentId,
  });
  const formQuery = {
    isPending: !!segmentId && query.isPending,
    isError: !!segmentId && query.isError,
    error: query.error,
    refetch: query.refetch,
  };
  useEffect(() => {
    if (query.data && !loaded.current) {
      loaded.current = true;
      setName(query.data.name);
      setDescription(query.data.description ?? '');
    }
  }, [query.data]);
  const destination = `/apps/${appId}/segments`;
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const origin = window.location.pathname;
    const result = await operation.write<{ data: Segment }>(
      `${destination}${segmentId ? `/${segmentId}` : ''}`,
      segmentId ? 'PUT' : 'POST',
      { name: name.trim(), description: description.trim() || null },
    );
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (segmentId) await query.refetch();
      if (window.location.pathname === origin) navigate(`${destination}/${result.data.id}`);
    }
  }
  async function remove() {
    if (!window.confirm(`Delete segment “${name}”? This will also remove its membership records.`))
      return;
    const origin = window.location.pathname;
    const result = await operation.write<{ success: boolean }>(
      `${destination}/${segmentId}`,
      'DELETE',
    );
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['directory', destination] });
      if (window.location.pathname === origin) navigate(destination);
    }
  }
  return (
    <PageFrame
      title={segmentId ? name || 'Segment details' : 'Create segment'}
      back={{ label: 'Back to segments', href: destination }}
      contentWidth={720}
    >
      <QueryState query={formQuery} loadingVariant="form">
        <Section padding={6}>
          <form onInput={() => setDirty(true)} onSubmit={(event) => void save(event)}>
            <FormLayout>
              <TextInput
                label="Segment name"
                isRequired
                value={name}
                onChange={(value) => setName(value.slice(0, 255))}
                isDisabled={operation.blocked()}
              />
              <TextArea
                label="Description"
                rows={3}
                value={description}
                onChange={setDescription}
                isDisabled={operation.blocked()}
              />
              <WriteFeedback operation={operation} />
              <Stack direction="horizontal" gap={2} wrap="wrap">
                <SubmitAction
                  label={operation.pending() ? 'Saving…' : 'Save segment'}

                  isLoading={operation.pending()}
                  isDisabled={operation.blocked()}
                />
                {segmentId && (
                  <Button
                    label="Delete segment"
                    type="button"
                    variant="destructive"
                    onClick={() => void remove()}
                    isDisabled={operation.blocked()}
                  />
                )}
              </Stack>
            </FormLayout>
          </form>
        </Section>
        {segmentId && <SegmentMembers />}
      </QueryState>
    </PageFrame>
  );
}
function SegmentMembers() {
  const { appId = '', segmentId = '' } = useParams();
  const operation = useWrite();
  const client = useQueryClient();
  const [adding, setAdding] = useState(false);
  const path = `/apps/${appId}/segments/${segmentId}/members`;
  const members = usePage<Member>(path);
  const users = usePage<User>(`/apps/${appId}/users`);
  async function change(id: string, add: boolean) {
    if (!add && !window.confirm('Remove this user from the segment?')) return;
    const result = await operation.write<{ success: boolean }>(
      `${path}/${id}`,
      add ? 'PUT' : 'DELETE',
    );
    if (result) await client.invalidateQueries({ queryKey: ['directory', path] });
  }
  return (
    <Section padding={6}>
      <Stack gap={3}>
        <Stack direction="horizontal" gap={2} wrap="wrap" align="center">
          <Heading level={2}>Segment members</Heading>
          <Button
            label={adding ? 'Close picker' : 'Add member'}
            aria-expanded={adding}
            onClick={() => setAdding((value) => !value)}
          />
        </Stack>
        <Text>
          Segment members are not the same as devices eligible at delivery time. A user may have
          several devices.
        </Text>
        <WriteFeedback operation={operation} />
        <QueryState query={members.query}>
          {members.query.data?.items.length ? (
            <Table density="balanced" hasHover aria-label="Segment members">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>User</TableHeaderCell>
                  <TableHeaderCell>External ID</TableHeaderCell>
                  <TableHeaderCell>Action</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.query.data.items.map((item) => (
                  <TableRow key={item.user_id}>
                    <TableCell>{item.name || `User #${item.user_id}`}</TableCell>
                    <TableCell>{item.external_id}</TableCell>
                    <TableCell>
                      <Button
                        label={`Remove ${item.name || item.user_id}`}
                        variant="secondary"
                        size="sm"
                        isDisabled={operation.blocked()}
                        onClick={() => void change(item.user_id, false)}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <EmptyState
              title="No members in this segment"
              description="Add users from this application to include them."
              isCompact
            />
          )}
          <Pager page={members} />
        </QueryState>
        {adding && (
          <Section variant="muted" padding={6}>
            <Stack gap={2}>
              <Heading level={3}>Select a user from this application</Heading>
              <QueryState query={users.query}>
                {users.query.data?.items.length ? (
                  users.query.data.items.map((user) => {
                    const alreadyMember =
                      members.query.data?.items.some((member) => member.user_id === user.id) ??
                      false;
                    return (
                      <Stack
                        key={user.id}
                        direction="horizontal"
                        gap={2}
                        align="center"
                        justify="between"
                      >
                        <Stack direction="horizontal" gap={1} align="center">
                          <Text>{user.name || user.external_id}</Text>
                          <Text type="supporting" color="secondary">
                            #{user.id}
                          </Text>
                        </Stack>
                        <Button
                          label={`Add ${user.name || user.external_id}`}
                          size="sm"
                          variant="secondary"
                          isDisabled={operation.blocked() || alreadyMember}
                          onClick={() => void change(user.id, true)}
                        />
                      </Stack>
                    );
                  })
                ) : (
                  <Text>No users are available.</Text>
                )}
                <Pager page={users} />
              </QueryState>
            </Stack>
          </Section>
        )}
      </Stack>
    </Section>
  );
}
