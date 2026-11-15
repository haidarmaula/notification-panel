import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowRightIcon, ArrowPathIcon } from '@heroicons/react/24/outline';
import { Button } from '@astryxdesign/core/Button';
import { Banner } from '@astryxdesign/core/Banner';
import { Card } from '@astryxdesign/core/Card';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Heading } from '@astryxdesign/core/Heading';
import { Icon } from '@astryxdesign/core/Icon';
import { Link } from '@astryxdesign/core/Link';
import { Grid } from '@astryxdesign/core/Grid';
import { SegmentedControl, SegmentedControlItem } from '@astryxdesign/core/SegmentedControl';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Table, proportional } from '@astryxdesign/core/Table';
import { Text } from '@astryxdesign/core/Text';
import { data } from '../../lib/api/client';
import type { Campaign, Page } from '../../lib/api/types';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
import { PageFrame } from '../../components/ui/PageFrame';

type Statistics = {
  schema_version: number;
  snapshot_complete: boolean;
  observation_coverage: string;
  counts: null | Record<string, number>;
};
type Delivery = {
  id: string;
  user_id: string;
  installation_id: string | null;
  status: string;
  sent_at: string | null;
  delivered_at: string | null;
  opened_at: string | null;
};
type Read = { id: string; user_id: string; read_at: string };
type HistoryRow = Record<string, unknown> & { id: string; user_id: string };

function timestamp(value: string | null | undefined) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isFinite(date.getTime())
    ? new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
    : '—';
}
function count(value: number | undefined) {
  return value !== undefined && Number.isSafeInteger(value) && value >= 0
    ? value.toLocaleString('en-GB')
    : '—';
}

export default function Results() {
  const { appId = '', campaignId = '' } = useParams();
  const [tab, setTab] = useState<'deliveries' | 'reads'>('deliveries');
  const [cursors, setCursors] = useState<string[]>([]);
  const path = `/apps/${appId}/campaigns/${campaignId}`;
  const campaign = useQuery({
    queryKey: ['campaign', appId, campaignId],
    queryFn: ({ signal }) => data<Campaign>(path, signal),
  });
  const resultsEnabled = campaign.isSuccess && !!campaign.data && campaign.data.status !== 'DRAFT';
  const statistics = useQuery({
    queryKey: ['statistics', appId, campaignId],
    queryFn: ({ signal }) => data<Statistics>(`${path}/statistics`, signal),
    enabled: resultsEnabled,
  });
  const history = useQuery({
    queryKey: ['history', appId, campaignId, tab, cursors.at(-1) ?? ''],
    enabled: resultsEnabled,
    queryFn: ({ signal }) =>
      data<Page<Delivery | Read>>(
        `${path}/${tab}?limit=20${cursors.length ? `&after_id=${encodeURIComponent(cursors.at(-1)!)}` : ''}`,
        signal,
      ),
  });
  const metrics = [
    { key: 'targeted_users', label: 'Targeted users', note: 'Snapshot at preparation' },
    {
      key: 'provider_accepted_devices',
      label: 'Accepted by provider',
      note: 'Not proof of device receipt',
    },
    {
      key: 'observed_devices',
      label: 'Reporting devices',
      note: 'Only devices that sent a receipt',
    },
    { key: 'opened_users', label: 'Users who opened', note: 'Unique users observed' },
  ];
  const items = (history.data?.items ?? []) as HistoryRow[];

  return (
    <PageFrame
      title="Delivery results"
      description={
        campaign.data?.status === 'DRAFT'
          ? campaign.data.title
          : 'Provider acceptance, device reports, and opens are recorded separately.'
      }
      back={{ label: 'Campaign details', href: path }}
      actions={
        <Button
          label="Refresh"
          variant="secondary"
          icon={<Icon icon={ArrowPathIcon} size="sm" />}
          isDisabled={campaign.isFetching || statistics.isFetching || history.isFetching}
          onClick={async () => {
            const refreshed = await campaign.refetch();
            if (refreshed.isSuccess && refreshed.data.status !== 'DRAFT') {
              void statistics.refetch({ cancelRefetch: false });
              void history.refetch({ cancelRefetch: false });
            }
          }}
        />
      }
    >
      <HStack gap={2} wrap="wrap" vAlign="center">
        <Text type="supporting" color="secondary">
          Campaign #{campaignId}
        </Text>
        {campaign.data && <Status value={campaign.data.status} />}
      </HStack>
      {campaign.isPending ? (
        <Loading />
      ) : campaign.isError ? (
        <Failure error={campaign.error} retry={() => void campaign.refetch()} />
      ) : campaign.data?.status === 'DRAFT' ? (
        <EmptyState
          title="Campaign has not been sent"
          description="Send or schedule this campaign from its detail page."
          actions={
            <Link href={path}>
              Review campaign <Icon icon={ArrowRightIcon} size="sm" />
            </Link>
          }
        />
      ) : (
        <VStack gap={5}>
          <Text aria-live="polite">
            Updated:{' '}
            {statistics.dataUpdatedAt
              ? new Date(statistics.dataUpdatedAt).toLocaleTimeString('en-GB')
              : 'not available'}
            . Times use your device time zone.
          </Text>
          <Collapsible trigger="A message arrived, but no receipt is shown?" defaultIsOpen={false}>
            <Text>
              Device reports depend on the recipient app. Check its session, API connection, and
              reporting of DELIVERED and OPENED events. A zero means the backend has not observed a
              receipt; it does not prove the message was not received. Do not resend only to refresh
              these counts.
            </Text>
          </Collapsible>
          {statistics.isPending ? (
            <Loading />
          ) : statistics.isError ? (
            <Failure error={statistics.error} retry={() => void statistics.refetch()} />
          ) : statistics.data?.schema_version !== 1 ||
            !statistics.data.snapshot_complete ||
            statistics.data.counts === null ? (
            <Banner
              status="warning"
              role="status"
              title="Statistics are incomplete"
              description="The audience snapshot is unavailable or this campaign uses an earlier format. This does not mean the result is zero."
            />
          ) : (
            <VStack gap={3}>
              <Grid columns={{ minWidth: 220, max: 4 }} gap={3}>
                {metrics.map((metric) => (
                  <Card key={metric.key}>
                    <VStack gap={1}>
                      <Text>{metric.label}</Text>
                      <Heading level={2}>{count(statistics.data.counts?.[metric.key])}</Heading>
                      <Text color="secondary">{metric.note}</Text>
                    </VStack>
                  </Card>
                ))}
              </Grid>
              <Text color="secondary">
                These counts use different units and coverage, so they cannot be added into one
                delivery total.
              </Text>
            </VStack>
          )}
          <HStack gap={3} wrap="wrap" hAlign="between" vAlign="center">
            <SegmentedControl
              value={tab}
              label="Results view"
              onChange={(value) => {
                if (value === 'deliveries' || value === 'reads') {
                  setTab(value);
                  setCursors([]);
                }
              }}
            >
              <SegmentedControlItem value="deliveries" label="Deliveries" />
              <SegmentedControlItem value="reads" label="Opens" />
            </SegmentedControl>
            <Text color="secondary">
              Time zone: {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </Text>
          </HStack>
          {history.isPending ? (
            <Loading />
          ) : history.isError ? (
            <Failure error={history.error} retry={() => void history.refetch()} />
          ) : items.length === 0 ? (
            <EmptyState
              title={`No ${tab === 'reads' ? 'open' : 'delivery'} records yet`}
              description="Records appear after data has been processed and received by the backend."
            />
          ) : (
            <Table
              data={items}
              idKey="id"
              columns={
                tab === 'deliveries'
                  ? [
                      {
                        key: 'user_id',
                        header: 'User',
                        width: proportional(1, { minWidth: 140 }),
                        renderCell: (item) => (
                          <>
                            #{item.user_id}
                            <Text color="secondary">Record #{item.id}</Text>
                          </>
                        ),
                      },
                      {
                        key: 'status',
                        header: 'Device status',
                        width: proportional(1, { minWidth: 160 }),
                        renderCell: (item) => <Status value={(item as Delivery).status} />,
                      },
                      {
                        key: 'sent_at',
                        header: 'Provider time',
                        width: proportional(1, { minWidth: 160 }),
                        renderCell: (item) => timestamp((item as Delivery).sent_at),
                      },
                      {
                        key: 'opened_at',
                        header: 'Opened',
                        width: proportional(1, { minWidth: 160 }),
                        renderCell: (item) => timestamp((item as Delivery).opened_at),
                      },
                    ]
                  : [
                      {
                        key: 'user_id',
                        header: 'User',
                        width: proportional(1, { minWidth: 140 }),
                        renderCell: (item) => (
                          <>
                            #{item.user_id}
                            <Text color="secondary">Record #{item.id}</Text>
                          </>
                        ),
                      },
                      {
                        key: 'read_at',
                        header: 'Open time',
                        width: proportional(1, { minWidth: 160 }),
                        renderCell: (item) => timestamp((item as Read).read_at),
                      },
                    ]
              }
            />
          )}
          <HStack gap={3} wrap="wrap" hAlign="between" vAlign="center">
            <Text>Page {cursors.length + 1}</Text>
            <HStack gap={2}>
              <Button
                label="Previous"
                variant="secondary"
                size="sm"
                isDisabled={!cursors.length || history.isFetching}
                onClick={() => setCursors((current) => current.slice(0, -1))}
              />
              <Button
                label="Next"
                variant="secondary"
                size="sm"
                isDisabled={!history.data?.next_after_id || history.isFetching}
                onClick={() => {
                  const next = history.data?.next_after_id;
                  if (next) setCursors((current) => [...current, next]);
                }}
              />
            </HStack>
          </HStack>
        </VStack>
      )}
    </PageFrame>
  );
}
