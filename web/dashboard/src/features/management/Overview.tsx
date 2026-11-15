import { Button } from '@astryxdesign/core/Button';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Icon } from '@astryxdesign/core/Icon';
import { List, ListItem } from '@astryxdesign/core/List';
import { Section } from '@astryxdesign/core/Section';
import { Stack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
} from '@astryxdesign/core/Table';
import { Link } from '@astryxdesign/core/Link';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  ChevronRightIcon,
  DocumentTextIcon,
  DevicePhoneMobileIcon,
  UserGroupIcon,
} from '@heroicons/react/24/outline';
import { data } from '../../lib/api/client';
import type { CampaignSummary, Page } from '../../lib/api/types';
import { PageFrame } from '../../components/ui/PageFrame';
import { Status } from '../../components/ui/Feedback';
import { QueryState } from './shared';

export default function Overview() {
  const { appId = '' } = useParams();
  const recent = useQuery({
    queryKey: ['recent-campaigns', appId],
    queryFn: ({ signal }) =>
      data<Page<CampaignSummary>>(`/apps/${appId}/campaigns?limit=5`, signal),
  });
  return (
    <PageFrame
      title="Overview"
      description="Continue campaign work and review delivery results."
      actions={
        <Button label="Create campaign" variant="primary" href={`/apps/${appId}/campaigns/new`} />
      }
    >
      <Stack gap={5}>
        <Section padding={6}>
          <Stack gap={3}>
            <Stack direction="horizontal" gap={2} align="center" justify="between" wrap="wrap">
              <Heading level={2}>Recent campaigns</Heading>
              <Link href={`/apps/${appId}/campaigns`}>View all campaigns</Link>
            </Stack>
            <QueryState query={recent}>
              {recent.data?.items.length ? (
                <Table aria-label="Recent campaigns">
                  <TableHeader>
                    <TableRow isHeaderRow>
                      <TableHeaderCell>Campaign</TableHeaderCell>
                      <TableHeaderCell>Status</TableHeaderCell>
                      <TableHeaderCell>Results</TableHeaderCell>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {recent.data.items.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell>
                          <Link href={`/apps/${appId}/campaigns/${item.id}`}>{item.title}</Link>{' '}
                          <Text size="sm">#{item.id}</Text>
                        </TableCell>
                        <TableCell>
                          <Status value={item.status} />
                        </TableCell>
                        <TableCell>
                          {item.status === 'DRAFT' ? (
                            'Not sent'
                          ) : (
                            <Link href={`/apps/${appId}/campaigns/${item.id}/results`}>
                              View results
                            </Link>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <EmptyState
                  title="No campaigns yet"
                  description="Create a draft campaign to start preparing a delivery."
                  actions={
                    <Button
                      label="Create campaign"
                      variant="primary"
                      href={`/apps/${appId}/campaigns/new`}
                    />
                  }
                />
              )}
            </QueryState>
          </Stack>
        </Section>
        <Section padding={6}>
          <Stack gap={3}>
            <Heading level={2}>Prepare a delivery</Heading>
            <List>
              <ListItem
                label="Message templates"
                description="Reuse message content in a new draft."
                startContent={<Icon icon={DocumentTextIcon} />}
                endContent={<Icon icon={ChevronRightIcon} />}
                href={`/apps/${appId}/templates`}
              />
              <ListItem
                label="Recipient segments"
                description="Manage user groups for campaign targeting."
                startContent={<Icon icon={UserGroupIcon} />}
                endContent={<Icon icon={ChevronRightIcon} />}
                href={`/apps/${appId}/segments`}
              />
              <ListItem
                label="Users and devices"
                description="Review recipient device registrations."
                startContent={<Icon icon={DevicePhoneMobileIcon} />}
                endContent={<Icon icon={ChevronRightIcon} />}
                href={`/apps/${appId}/audience`}
              />
            </List>
          </Stack>
        </Section>
      </Stack>
    </PageFrame>
  );
}
