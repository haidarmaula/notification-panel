import { AndroidNotificationPreview } from './AndroidNotificationPreview';
import { useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowTopRightOnSquareIcon,
  CalendarIcon,
  PencilSquareIcon,
  PaperAirplaneIcon,
  TrashIcon,
} from '@heroicons/react/24/outline';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { Dialog } from '@astryxdesign/core/Dialog';
import { Grid } from '@astryxdesign/core/Grid';
import { Heading } from '@astryxdesign/core/Heading';
import { Icon } from '@astryxdesign/core/Icon';
import { Link } from '@astryxdesign/core/Link';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Section } from '@astryxdesign/core/Section';
import { Text } from '@astryxdesign/core/Text';
import { DateTimeInput, type ISODateTimeString } from '@astryxdesign/core/DateTimeInput';
import { useAuth } from '../../app/auth-context';
import { ApiError, data, errorMessage, request } from '../../lib/api/client';
import type { Application, Campaign } from '../../lib/api/types';
import { Failure, Loading, RecipientLabel, Status } from '../../components/ui/Feedback';
import { PageFrame } from '../../components/ui/PageFrame';

type Operation = 'send' | 'schedule' | 'delete';

export default function Detail() {
  const { appId = '', campaignId = '' } = useParams();
  const application = useQuery({
    queryKey: ['application', appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${appId}`, signal),
    enabled: Boolean(appId),
  });
  const auth = useAuth();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [action, setAction] = useState<Operation | null>(null);
  const [scheduled, setScheduled] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [accepted, setAccepted] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const currentIdentity = useRef({ appId, campaignId });
  currentIdentity.current = { appId, campaignId };
  const path = `/apps/${appId}/campaigns/${campaignId}`;
  const query = useQuery({
    queryKey: ['campaign', appId, campaignId],
    queryFn: ({ signal }) => data<Campaign>(path, signal),
    refetchInterval: (entry) =>
      (accepted && entry.state.data?.status === 'DRAFT') ||
      ['QUEUED', 'PROCESSING'].includes(entry.state.data?.status ?? '')
        ? 5000
        : false,
  });

  async function confirm() {
    if (pending || uncertain || accepted || !action) return;
    const operation = action;
    const currentAppId = appId;
    const currentCampaignId = campaignId;
    setError('');
    setPending(true);
    try {
      let body: unknown;
      if (operation === 'schedule') {
        const time = new Date(scheduled);
        if (!Number.isFinite(time.getTime()) || time.getTime() <= Date.now()) {
          setError('Choose a delivery time in the future.');
          return;
        }
        body = { scheduled_at: time.toISOString() };
      }
      await request(`/bff/api${path}${operation === 'delete' ? '' : `/${operation}`}`, {
        method: operation === 'delete' ? 'DELETE' : 'POST',
        body,
        csrf: auth.csrf(),
        idempotencyKey:
          operation === 'send'
            ? `hello-dashboard:${currentAppId}:${currentCampaignId}:send`
            : undefined,
      });
      if (operation !== 'delete') setAccepted(true);
      await client.invalidateQueries({ queryKey: ['campaigns', currentAppId] });
      await client.invalidateQueries({ queryKey: ['recent-campaigns', currentAppId] });
      await client.invalidateQueries({
        queryKey: ['campaign', currentAppId, currentCampaignId],
      });
      if (
        currentIdentity.current.appId !== currentAppId ||
        currentIdentity.current.campaignId !== currentCampaignId
      )
        return;
      setAction(null);
      if (operation === 'delete') navigate(`/apps/${currentAppId}/campaigns`);
      else
        setSuccess(
          operation === 'send'
            ? 'The send request was accepted. Device results will be recorded separately.'
            : 'The delivery schedule was accepted.',
        );
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      if (
        cause instanceof ApiError &&
        (cause.status === 0 || (cause.status >= 500 && cause.status !== 503))
      ) {
        setUncertain(true);
        void query.refetch();
      }
      setError(errorMessage(cause));
    } finally {
      setPending(false);
    }
  }

  function open(value: Operation) {
    if (accepted || uncertain) return;
    setError('');
    setScheduled('');
    setAction(value);
  }

  const campaign = query.data;
  return (
    <PageFrame
      title={campaign?.title ?? 'Campaign details'}
      description={
        campaign ? (
          <>
            Campaign #{campaign.id} · <Status value={campaign.status} />
          </>
        ) : undefined
      }
      back={{ label: 'Back to campaigns', href: `/apps/${appId}/campaigns` }}
      actions={
        campaign ? (
          campaign.status === 'DRAFT' && !accepted && !uncertain ? (
            <>
              <Button
                label="Edit draft"
                variant="secondary"
                icon={<Icon icon={PencilSquareIcon} size="sm" />}
                href={`${path}/edit`}
              />
              <Button
                label="Schedule delivery"
                variant="secondary"
                icon={<Icon icon={CalendarIcon} size="sm" />}
                onClick={() => open('schedule')}
              />
              <Button
                label="Review send"
                variant="primary"
                icon={<Icon icon={PaperAirplaneIcon} size="sm" />}
                onClick={() => open('send')}
              />
            </>
          ) : (
            <Button
              label="View results"
              variant="primary"
              icon={<Icon icon={ArrowTopRightOnSquareIcon} size="sm" />}
              href={`${path}/results`}
            />
          )
        ) : undefined
      }
    >
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <Failure error={query.error} retry={() => void query.refetch()} />
      ) : campaign ? (
        <VStack gap={6}>
          {uncertain && (
            <Text role="alert">
              The action outcome is uncertain. Check the latest status before trying again.
            </Text>
          )}
          {accepted && campaign.status === 'DRAFT' && (
            <Text role="status">Request accepted. Waiting for the campaign status to update…</Text>
          )}
          {success && <Text role="status">{success}</Text>}
          <Grid columns={{ minWidth: 360, max: 2 }} gap={6} align="start">
            <Section padding={6}>
              <VStack gap={4}>
                <Heading level={2}>Notification content</Heading>
                <Text>
                  <strong>Title</strong>
                  <br />
                  {campaign.title}
                </Text>
                <Text>
                  <strong>Message</strong>
                  <br />
                  {campaign.body}
                </Text>
                {campaign.template_name && (
                  <Text>
                    <strong>Template</strong>
                    <br />
                    {campaign.template_name}
                  </Text>
                )}
                <Heading level={2}>Recipients</Heading>
                <VStack gap={2} role="group" aria-label="Recipients">
                  {campaign.targets.map((target, index) => (
                    <RecipientLabel
                      key={`${target.type}:${target.id ?? index}`}
                      appId={appId}
                      target={target}
                    />
                  ))}
                </VStack>
                <Text color="secondary">
                  These targets are not an estimate of device count. The backend checks eligibility
                  during preparation.
                </Text>
              </VStack>
            </Section>
            <Section padding={6}>
              <VStack gap={4}>
                <Heading level={2}>Notification preview</Heading>
                <AndroidNotificationPreview
                  applicationName={application.data?.name ?? 'Application'}
                  title={campaign.title}
                  body={campaign.body}
                  context="review"
                />
              </VStack>
            </Section>
          </Grid>
          <HStack gap={3} wrap="wrap" hAlign="between">
            <Link href={`${path}/results`}>
              Campaign history and results <Icon icon={ArrowTopRightOnSquareIcon} size="sm" />
            </Link>
            {campaign.status === 'DRAFT' && !accepted && !uncertain && (
              <Button
                label="Delete draft"
                variant="destructive"
                icon={<Icon icon={TrashIcon} size="sm" />}
                onClick={() => open('delete')}
              />
            )}
          </HStack>
        </VStack>
      ) : null}
      <Dialog
        isOpen={action !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen && !pending) setAction(null);
        }}
        purpose="form"
      >
        <VStack gap={4}>
          <Heading level={2}>
            {action === 'delete'
              ? 'Delete this draft?'
              : action === 'schedule'
                ? 'Schedule campaign'
                : 'Review before sending'}
          </Heading>
          <Text>
            {action === 'delete'
              ? 'A deleted draft cannot be restored from the dashboard.'
              : 'This action processes a real campaign in the selected application.'}
          </Text>
          <Card>
            <VStack gap={2}>
              <Text>
                <strong>{campaign?.title}</strong>
              </Text>
              <Text>{campaign?.body}</Text>
              {campaign?.targets.map((target, index) => (
                <RecipientLabel
                  key={`${target.type}:${target.id ?? index}`}
                  appId={appId}
                  target={target}
                />
              ))}
            </VStack>
          </Card>
          {action === 'schedule' && (
            <DateTimeInput
              label="Delivery time"
              presentation="native"
              value={scheduled ? (scheduled as ISODateTimeString) : undefined}
              onChange={(value) => setScheduled(value ?? '')}
              isRequired
              isDisabled={pending || uncertain || accepted}
            />
          )}
          {action === 'schedule' && (
            <Text color="secondary">
              Device time zone: {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </Text>
          )}
          {action === 'send' && (
            <Text color="secondary">
              An accepted request does not mean a message reached a device. Sending limits still
              apply.
            </Text>
          )}
          {error && <Text role="alert">{error}</Text>}
          <HStack gap={2} hAlign="end">
            <Button
              label="Back"
              variant="secondary"
              isDisabled={pending}
              onClick={() => setAction(null)}
            />
            <Button
              label={
                pending
                  ? 'Processing…'
                  : action === 'delete'
                    ? 'Delete draft'
                    : action === 'schedule'
                      ? 'Confirm schedule'
                      : 'Confirm send'
              }
              variant={action === 'delete' ? 'destructive' : 'primary'}
              isLoading={pending}
              isDisabled={pending || uncertain || accepted}
              onClick={() => void confirm()}
            />
          </HStack>
        </VStack>
      </Dialog>
    </PageFrame>
  );
}
