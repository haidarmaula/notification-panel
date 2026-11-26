import { Heading, Text } from '@astryxdesign/core/Text';
import { Link } from '@astryxdesign/core/Link';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { MetadataList, MetadataListItem } from '@astryxdesign/core/MetadataList';
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
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { data } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/staff';
import { PageFrame } from '../../components/ui/PageFrame';
import { Status } from '../../components/ui/Feedback';
import { date, hasPageNavigation, usePage } from './data-hooks';
import { Pager, QueryState } from './shared';
type User = components['schemas']['User'];
type Device = components['schemas']['Device'];
export default function Audience() {
  const { appId = '' } = useParams();
  const page = usePage<User>(`/apps/${appId}/users`);
  return (
    <PageFrame
      footer={hasPageNavigation(page) ? <Pager page={page} /> : undefined}
      title="Users and devices"
      description="Review users and devices in the selected application."
      actions={
        <Link href={`/apps/${appId}/segments`} isStandalone>
          Manage segments
        </Link>
      }
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label="Application users">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>User</TableHeaderCell>
                  <TableHeaderCell>External ID</TableHeaderCell>
                  <TableHeaderCell>Status</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Link href={`/apps/${appId}/audience/${item.id}`}>
                        {item.name || `User #${item.id}`}
                      </Link>{' '}
                      <Text type="supporting" color="secondary">
                        #{item.id}
                      </Text>
                    </TableCell>
                    <TableCell>{item.external_id}</TableCell>
                    <TableCell>
                      <Status value={item.status} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No users available"
            description="Users appear after syncing through the application integration."
          />
        )}
      </QueryState>
    </PageFrame>
  );
}
export function UserDetail() {
  const { appId = '', userId = '' } = useParams();
  const user = useQuery({
    queryKey: ['user', appId, userId],
    queryFn: ({ signal }) => data<User>(`/apps/${appId}/users/${userId}`, signal),
  });
  const devices = usePage<Device>(`/apps/${appId}/users/${userId}/devices`);
  return (
    <PageFrame
      title={user.data?.name || user.data?.external_id || 'User details'}
      back={{ label: 'Back to users', href: `/apps/${appId}/audience` }}
    >
      <QueryState query={user}>
        <Section padding={6}>
          <Stack gap={4}>
            <MetadataList label={{ position: 'start' }}>
              <MetadataListItem label="Email">
                {user.data?.email || 'Email unavailable'}
              </MetadataListItem>
              <MetadataListItem label="External ID">{user.data?.external_id}</MetadataListItem>
              <MetadataListItem label="Status">
                <Status value={user.data?.status ?? 'UNKNOWN'} />
              </MetadataListItem>
            </MetadataList>
            <Heading level={2}>Registered devices</Heading>
            <Text>
              An active status indicates an active registration. It does not mean the device is
              online or will receive a message.
            </Text>
            <QueryState query={devices.query}>
              {devices.query.data?.items.length ? (
                <Table density="balanced" hasHover aria-label="Registered devices">
                  <TableHeader>
                    <TableRow isHeaderRow>
                      <TableHeaderCell>Device</TableHeaderCell>
                      <TableHeaderCell>Platform</TableHeaderCell>
                      <TableHeaderCell>Registration</TableHeaderCell>
                      <TableHeaderCell>Last seen</TableHeaderCell>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {devices.query.data.items.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell>#{item.id}</TableCell>
                        <TableCell>{item.platform}</TableCell>
                        <TableCell>
                          <Status value={item.is_active ? 'ACTIVE' : 'INACTIVE'} />
                        </TableCell>
                        <TableCell>{date(item.last_seen_at)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <EmptyState
                  title="No devices registered"
                  description="This user has no registered devices."
                  isCompact
                />
              )}
              <Pager page={devices} />
            </QueryState>
          </Stack>
        </Section>
      </QueryState>
    </PageFrame>
  );
}
