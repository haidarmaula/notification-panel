import { CreateAction } from '../../components/ui/Actions';
import { useState, type FormEvent } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowRightIcon, MegaphoneIcon } from '@heroicons/react/24/outline';
import { Button } from '@astryxdesign/core/Button';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Heading } from '@astryxdesign/core/Heading';
import { Icon } from '@astryxdesign/core/Icon';
import { Link } from '@astryxdesign/core/Link';
import { Layout, LayoutContent, LayoutFooter, LayoutHeader } from '@astryxdesign/core/Layout';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Table, proportional, pixel } from '@astryxdesign/core/Table';
import { Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { data } from '../../lib/api/client';
import type { CampaignSummary, Page } from '../../lib/api/types';
import { Failure, Loading, Status } from '../../components/ui/Feedback';

const campaignIdPattern = /^[1-9][0-9]{0,18}$/;
const maxCampaignId = 9223372036854775807n;

export default function Campaigns({ results = false }: { results?: boolean }) {
  const { appId = '' } = useParams();
  const navigate = useNavigate();
  const [search, setSearch] = useSearchParams();
  const [lookup, setLookup] = useState('');
  const [lookupError, setLookupError] = useState('');
  const cursors =
    typeof search.get('pages') === 'string' &&
    (search.get('pages')?.length ?? 0) < 2000 &&
    /^[1-9][0-9]*(,[1-9][0-9]*)*$/.test(search.get('pages') ?? '')
      ? (search.get('pages') ?? '').split(',')
      : [];
  const setCursors = (value: string[]) =>
    setSearch((current) => {
      const next = new URLSearchParams(current);
      if (value.length) next.set('pages', value.join(','));
      else next.delete('pages');
      return next;
    });
  const query = useQuery({
    queryKey: ['campaigns', appId, cursors.at(-1) ?? ''],
    queryFn: ({ signal }) =>
      data<Page<CampaignSummary>>(
        `/apps/${appId}/campaigns?limit=20${cursors.length ? `&after_id=${encodeURIComponent(cursors.at(-1)!)}` : ''}`,
        signal,
      ),
  });
  const destination = (id: string) => `/apps/${appId}/campaigns/${id}${results ? '/results' : ''}`;
  const rows = (query.data?.items ?? []) as (CampaignSummary & Record<string, unknown>)[];

  function openCampaign(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const id = lookup.trim();
    if (!id) {
      setLookupError('Enter a campaign ID.');
      return;
    }
    if (!campaignIdPattern.test(id)) {
      setLookupError('Enter 1 to 19 digits without a leading zero.');
      return;
    }
    if (BigInt(id) > maxCampaignId) {
      setLookupError(`Enter a campaign ID of ${maxCampaignId} or less.`);
      return;
    }
    navigate(destination(id));
  }

  return (
    <Layout
      height="fill"
      padding={6}
      header={
        <LayoutHeader role="region" label="Page header">
          <HStack gap={4} wrap="wrap" hAlign="between" vAlign="center">
            <VStack gap={1}>
              <Heading level={1}>{results ? 'Delivery logs' : 'Campaigns'}</Heading>
              <Text color="secondary">
                {results
                  ? 'Choose a campaign to review provider, device, and open records.'
                  : 'Manage campaign drafts, schedules, and delivery results.'}
              </Text>
            </VStack>
            <CreateAction label="Create campaign" href={`/apps/${appId}/campaigns/new`} />
          </HStack>
        </LayoutHeader>
      }
      footer={
        <LayoutFooter hasDivider role="region" label="Page footer">
          <HStack gap={4} wrap="wrap" hAlign="between" vAlign="center">
            <Text>Messages are sent only after you review and confirm the campaign.</Text>
            {!query.isPending &&
              !query.isError &&
              (!!query.data?.items.length || cursors.length > 0) && (
                <HStack gap={3} wrap="wrap" hAlign="between" vAlign="center">
                  <Text>Page {cursors.length + 1}</Text>
                  <HStack gap={2}>
                    <Button
                      label="Previous"
                      variant="secondary"
                      size="sm"
                      isDisabled={!cursors.length || query.isFetching}
                      onClick={() => setCursors(cursors.slice(0, -1))}
                    />
                    <Button
                      label="Next"
                      variant="secondary"
                      size="sm"
                      isDisabled={!query.data?.next_after_id || query.isFetching}
                      onClick={() => {
                        const cursor = query.data?.next_after_id;
                        if (cursor) setCursors([...cursors, cursor]);
                      }}
                    />
                  </HStack>
                </HStack>
              )}
          </HStack>
        </LayoutFooter>
      }
    >
      <LayoutContent>
        <VStack gap={4}>
          <HStack gap={4} wrap="wrap" hAlign="start" vAlign="center">
            <Collapsible
              trigger={
                <Text type="supporting" color="secondary">
                  Open by campaign ID
                </Text>
              }
              defaultIsOpen={false}
            >
              <form onSubmit={openCampaign}>
                <HStack gap={2} wrap="wrap" vAlign="end">
                  <TextInput
                    label="Open by campaign ID"
                    type="text"
                    inputMode="numeric"
                    enterKeyHint="go"
                    value={lookup}
                    onChange={(value) => {
                      setLookup(value);
                      setLookupError('');
                    }}
                    placeholder="Example: 13"
                    isRequired
                    status={lookupError ? { type: 'error', message: lookupError } : undefined}
                  />
                  <Button label="Open" variant="secondary" type="submit" />
                </HStack>
              </form>
            </Collapsible>
          </HStack>
          {query.isPending ? (
            <Loading rows={5} />
          ) : query.isError ? (
            <Failure error={query.error} retry={() => void query.refetch()} />
          ) : rows.length === 0 ? (
            <EmptyState
              icon={<Icon icon={MegaphoneIcon} size="md" />}
              title="Start with your first message"
              description="Create a draft, choose recipients, then review before sending."
              actions={
                <CreateAction
                  label="Create your first campaign"
                  href={`/apps/${appId}/campaigns/new`}
                />
              }
            />
          ) : (
            <Table
              density="balanced"
              hasHover
              data={rows}
              idKey="id"

              columns={[
                {
                  key: 'title',
                  width: proportional(3, { minWidth: 220 }),
                  header: 'Campaign name',
                  renderCell: (item) => (
                    <Link href={destination(item.id)}>
                      <VStack gap={0}>
                        <Text>{item.title}</Text>
                        <Text color="secondary">Campaign #{item.id}</Text>
                      </VStack>
                    </Link>
                  ),
                },
                {
                  key: 'status',
                  width: pixel(150),
                  header: 'Status',
                  renderCell: (item) => <Status value={item.status} />,
                },
                {
                  key: 'action',
                  width: pixel(112),
                  header: 'Open',
                  renderCell: (item) => (
                    <Link href={destination(item.id)} label={`Open ${item.title}`}>
                      <Icon icon={ArrowRightIcon} size="sm" />
                    </Link>
                  ),
                },
              ]}
            />
          )}
        </VStack>
      </LayoutContent>
    </Layout>
  );
}
