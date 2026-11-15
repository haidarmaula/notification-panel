import { useQuery } from '@tanstack/react-query';
import { VStack } from '@astryxdesign/core/Stack';
import { Text } from '@astryxdesign/core/Text';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Token, type TokenColor } from '@astryxdesign/core/Token';
import { data, errorMessage } from '../../lib/api/client';
export { Loading } from './Loading';
export function RecipientLabel({
  appId,
  target,
  name: suppliedName,
}: {
  appId: string;
  target: { type: string; id?: string | null };
  name?: string;
}) {
  const named = target.type === 'INDIVIDUAL' || target.type === 'SEGMENT';
  const recipient = useQuery({
    queryKey: ['recipient-name', appId, target.type, target.id],
    queryFn: ({ signal }) =>
      data<{ name?: string | null; external_id?: string }>(
        `/apps/${appId}/${target.type === 'SEGMENT' ? 'segments' : 'users'}/${target.id}`,
        signal,
      ),
    enabled: named && !!target.id && !suppliedName,
  });
  const kind =
    target.type === 'SEGMENT' ? 'Segment' : target.type === 'INDIVIDUAL' ? 'User' : 'Saved target';
  const name =
    suppliedName ||
    (recipient.isSuccess ? recipient.data?.name || recipient.data?.external_id : undefined);
  return (
    <VStack gap={1}>
      <Text>
        {target.type === 'BROADCAST' ? 'All recipients in this application' : name || kind}
      </Text>
      {target.id && (
        <Text type="supporting" color="secondary">
          {name ? `${kind} ` : ''}#{target.id}
        </Text>
      )}
    </VStack>
  );
}
export function Failure({ error, retry }: { error: unknown; retry?: () => void }) {
  return (
    <Banner
      status="error"
      title="Unable to complete this request"
      description={errorMessage(error)}
      endContent={retry ? <Button label="Retry" onClick={retry} /> : undefined}
    />
  );
}
const labels: Record<string, string> = {
  DRAFT: 'Draft',
  SCHEDULED: 'Scheduled',
  QUEUED: 'Queued',
  PROCESSING: 'Processing',
  COMPLETED: 'Completed',
  FAILED: 'Failed',
  SENT: 'Accepted by provider',
  DELIVERED: 'Observed on device',
  OPENED: 'Opened',
  PENDING: 'Pending',
  ACTIVE: 'Active',
  INACTIVE: 'Inactive',
  HELD: 'Held',
  PROVISIONING: 'Provisioning',
  RETIRED: 'Retired',
};
export function Status({ value }: { value: string }) {
  const color: TokenColor = ['ACTIVE', 'COMPLETED', 'DELIVERED', 'OPENED'].includes(value)
    ? 'green'
    : ['FAILED', 'RETIRED'].includes(value)
      ? 'red'
      : ['QUEUED', 'PROCESSING', 'SCHEDULED'].includes(value)
        ? 'blue'
        : 'gray';
  return <Token label={labels[value] ?? value} color={color} size="sm" />;
}
