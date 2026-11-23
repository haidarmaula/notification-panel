import { useMemo, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Heading, Text } from '@astryxdesign/core/Text';
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
import { useAuth } from '../app/auth-context';
import { isLocalTestDevicesPath } from '../app/local-test-devices';
import { PageFrame } from '../components/ui/PageFrame';
import { Status } from '../components/ui/Feedback';

type Device = {
  installation_id: string;
  platform: string;
  status: string;
  name: string;
  created_at?: string | null;
  last_seen_at?: string | null;
};
type TestResult = {
  campaign_id: string;
  status: string;
  accepted_at?: string | null;
  provider_accepted_at?: string | null;
  device_observed_at?: string | null;
  reason?: string | null;
};
type TestDeviceStatus = {
  ready: boolean;
  origin: string | null;
  apk_url: string | null;
  apk_status: 'ready' | 'missing' | 'building' | 'failed';
  devices: Device[];
  target: Device | null;
  last_test: TestResult | null;
  issues: string[];
};
type PairingResult = { qr_svg: string; expires_at: string };
type SendResult = { campaign_id: string; status: string };

class HelperError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function helperRequest<T>(
  path: string,
  options: { signal?: AbortSignal; method?: string; body?: unknown; csrf?: string } = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      method: options.method ?? 'GET',
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: options.signal,
      headers: {
        ...(options.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        ...(options.csrf ? { 'X-CSRF-Token': options.csrf } : {}),
      },
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new HelperError('The request outcome is unknown. Refresh status before retrying.', 0);
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    if (response.ok)
      throw new HelperError('The local helper returned an unreadable response.', 502);
    payload = null;
  }
  if (!response.ok) {
    const message =
      typeof payload === 'object' &&
      payload &&
      'message' in payload &&
      typeof payload.message === 'string'
        ? payload.message
        : `The local helper returned HTTP ${response.status}.`;
    throw new HelperError(message, response.status);
  }
  return payload as T;
}

function date(value?: string | null) {
  if (!value) return 'Not recorded';
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

function pairingImage(svg: string) {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

export default function TestDevices() {
  const auth = useAuth();
  const client = useQueryClient();
  const enabled = isLocalTestDevicesPath(window.location.pathname);
  const [pairing, setPairing] = useState<PairingResult | null>(null);
  const [title, setTitle] = useState('Hello FCM test');
  const [body, setBody] = useState('This is a test notification from the local dashboard.');
  const [renamingId, setRenamingId] = useState('');
  const [deviceName, setDeviceName] = useState('');
  const [notice, setNotice] = useState('');
  const [failure, setFailure] = useState('');
  const [sendUncertain, setSendUncertain] = useState(false);
  const key = ['hello-dev-test-devices'];
  const status = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => helperRequest<TestDeviceStatus>('/__hello_dev/status', { signal }),
    enabled,
    refetchInterval: 3000,
  });
  const refresh = () => client.invalidateQueries({ queryKey: key });
  const updateStatus = (next: TestDeviceStatus) => client.setQueryData(key, next);
  const mutation = useMutation({
    mutationFn: async ({ path, body: requestBody }: { path: string; body?: unknown }) =>
      helperRequest<TestDeviceStatus | PairingResult | SendResult>(path, {
        method: 'POST',
        body: requestBody,
        csrf: auth.csrf(),
      }),
    onSuccess: (result, variables) => {
      setFailure('');
      if (variables.path === '/__hello_dev/pair') {
        setPairing(result as PairingResult);
        setNotice('A new one-use pairing QR is ready.');
      } else if (variables.path === '/__hello_dev/send') {
        const sent = result as SendResult;
        setNotice(`Test campaign ${sent.campaign_id} was accepted with status ${sent.status}.`);
      } else if (variables.path === '/__hello_dev/build-apk') {
        setNotice('APK build requested. This page will update when the build finishes.');
      } else {
        updateStatus(result as TestDeviceStatus);
        if (variables.path === '/__hello_dev/forget') {
          setNotice('Device forgotten.');
        } else if (variables.path === '/__hello_dev/name') {
          setRenamingId('');
          setDeviceName('');
          setNotice('Device name updated.');
        } else {
          setNotice('Test target updated.');
        }
      }
      void refresh();
    },
    onError: (error) => {
      setNotice('');
      if (
        mutation.variables?.path === '/__hello_dev/send' &&
        error instanceof HelperError &&
        error.status === 0
      ) {
        setSendUncertain(true);
      }
      setFailure(error instanceof Error ? error.message : 'The local helper request failed.');
    },
  });
  const deviceRows = useMemo(() => status.data?.devices ?? [], [status.data?.devices]);
  const apkHref = useMemo(() => {
    const value = status.data?.apk_url;
    if (!value) return undefined;
    try {
      const url = new URL(value, window.location.origin);
      return url.origin === window.location.origin ? url.href : undefined;
    } catch {
      return undefined;
    }
  }, [status.data?.apk_url]);

  function run(path: string, requestBody?: unknown) {
    setFailure('');
    setNotice('');
    if (path === '/__hello_dev/pair') setPairing(null);
    mutation.mutate({ path, body: requestBody });
  }

  function forget(device: Device) {
    if (!window.confirm(`Forget ${device.name}? This phone can pair again with a new QR.`)) return;
    run('/__hello_dev/forget', { installation_id: device.installation_id });
  }

  function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const target = status.data?.target;
    if (!target) return;
    run('/__hello_dev/send', {
      installation_id: target.installation_id,
      title: title.trim(),
      body: body.trim(),
    });
  }

  async function checkUncertainSend() {
    const result = await status.refetch();
    if (result.isSuccess) {
      setSendUncertain(false);
      setFailure('');
      setNotice('Latest test status refreshed. Review the receipt evidence before sending again.');
    }
  }

  return (
    <PageFrame
      title="Test Devices"
      description="Pair a local Android device and send a real FCM test. Provider acceptance and device receipt are reported as separate evidence."
      contentWidth={1120}
    >
      {!enabled && (
        <Banner
          status="warning"
          title="Local test tools are unavailable here"
          description="Open the development dashboard at localhost:5273 with VITE_HELLO_REAL_FCM=1."
        />
      )}
      {failure && <Banner status="error" title="Local test request failed" description={failure} />}
      {sendUncertain && (
        <Banner
          status="warning"
          title="Test send outcome is unknown"
          description="The connection ended before the helper confirmed the send. Review the latest test and receipt evidence before sending again."
          endContent={
            <Button
              label="Refresh test status"
              onClick={() => void checkUncertainSend()}
              isLoading={status.isFetching}
            />
          }
        />
      )}
      {notice && <Banner status="info" title="Test device update" description={notice} />}
      {status.isError && enabled && (
        <Banner
          status="error"
          title="Helper status unavailable"
          description={
            status.error instanceof Error ? status.error.message : 'Unable to read helper status.'
          }
          endContent={<Button label="Retry status" onClick={() => void status.refetch()} />}
        />
      )}
      {status.data && (
        <>
          {!status.data.ready && (
            <Banner
              status="warning"
              title="Pairing over Wi-Fi is unavailable"
              description="Resolve the Wi-Fi issue below before adding or reconnecting a phone."
            />
          )}
          {status.data.issues.length > 0 && (
            <Section padding={6}>
              <Stack gap={4}>
                <Heading level={2}>Diagnostics</Heading>
                <Stack gap={2}>
                  {status.data.issues.map((issue, index) => (
                    <Text key={`${index}-${issue}`}>{issue}</Text>
                  ))}
                </Stack>
              </Stack>
            </Section>
          )}
          <Section padding={6}>
            <Stack gap={4}>
              <Heading level={2}>Development app</Heading>
              <Text>
                APK status: <Status value={status.data.apk_status.toUpperCase()} />
              </Text>
              {status.data.apk_status === 'ready' && apkHref && (
                <Button label="Download development APK" href={apkHref} />
              )}
              {status.data.apk_status === 'ready' && !apkHref && (
                <Text>The APK is ready, but its same-origin download link is unavailable.</Text>
              )}
              <Button
                label={
                  status.data.apk_status === 'building'
                    ? 'Building Android development APK…'
                    : status.data.apk_status === 'ready'
                      ? 'Rebuild APK'
                      : 'Build Android development APK'
                }
                onClick={() => run('/__hello_dev/build-apk')}
                isDisabled={status.data.apk_status === 'building' || mutation.isPending}
                isLoading={
                  status.data.apk_status === 'building' ||
                  (mutation.variables?.path === '/__hello_dev/build-apk' && mutation.isPending)
                }
              />
              {status.data.origin && (
                <Text type="supporting" color="secondary">
                  Local origin: {status.data.origin}
                </Text>
              )}
            </Stack>
          </Section>
          <Section padding={6}>
            <Stack gap={4}>
              <Heading level={2}>Add device</Heading>
              <Text>
                Build and install the Android development APK, then create a one-use QR to enroll a
                phone.
              </Text>
              <Button
                label={
                  mutation.isPending && mutation.variables?.path === '/__hello_dev/pair'
                    ? 'Creating QR…'
                    : pairing
                      ? 'Create new QR'
                      : 'Create pairing QR'
                }
                onClick={() => run('/__hello_dev/pair')}
                isDisabled={!enabled || !status.data.ready || mutation.isPending}
                isLoading={mutation.isPending && mutation.variables?.path === '/__hello_dev/pair'}
              />
            </Stack>
          </Section>
          {pairing && (
            <Section padding={6}>
              <Stack gap={3}>
                <Heading level={2}>Pair this device</Heading>
                <Text>Scan this one-use QR in the Test Devices app before it expires.</Text>
                <img
                  src={pairingImage(pairing.qr_svg)}
                  alt="One-use device pairing QR code"
                  width={256}
                  height={256}
                />
                <Text type="supporting" color="secondary">
                  Expires {date(pairing.expires_at)}
                </Text>
              </Stack>
            </Section>
          )}
          <Section padding={6}>
            <Stack gap={4}>
              <Heading level={2}>Registered devices</Heading>
              {deviceRows.length ? (
                <Table density="balanced" hasHover aria-label="Test devices">
                  <TableHeader>
                    <TableRow isHeaderRow>
                      <TableHeaderCell>Device</TableHeaderCell>
                      <TableHeaderCell>Platform</TableHeaderCell>
                      <TableHeaderCell>Status</TableHeaderCell>
                      <TableHeaderCell>Last seen</TableHeaderCell>
                      <TableHeaderCell>Actions</TableHeaderCell>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {deviceRows.map((device) => {
                      const selected =
                        status.data?.target?.installation_id === device.installation_id;
                      return (
                        <TableRow key={device.installation_id}>
                          <TableCell>
                            <Stack gap={1}>
                              {renamingId === device.installation_id ? (
                                <TextInput
                                  label={`Name for device ${device.installation_id.slice(0, 8)}`}
                                  value={deviceName}
                                  onChange={(value) => setDeviceName(value.slice(0, 60))}
                                  isRequired
                                  isDisabled={mutation.isPending}
                                />
                              ) : (
                                <Text>{device.name || 'Android device'}</Text>
                              )}
                              <Text type="supporting" color="secondary">
                                {device.installation_id.slice(0, 8)}
                              </Text>
                            </Stack>
                          </TableCell>
                          <TableCell>{device.platform}</TableCell>
                          <TableCell>
                            <Status value={device.status} />
                          </TableCell>
                          <TableCell>{date(device.last_seen_at)}</TableCell>
                          <TableCell>
                            <Stack direction="horizontal" gap={2} wrap="wrap">
                              {renamingId === device.installation_id ? (
                                <>
                                  <Button
                                    label="Save name"
                                    variant="secondary"
                                    onClick={() =>
                                      run('/__hello_dev/name', {
                                        installation_id: device.installation_id,
                                        name: deviceName.trim(),
                                      })
                                    }
                                    isDisabled={!deviceName.trim() || mutation.isPending}
                                  />
                                  <Button
                                    label="Cancel rename"
                                    variant="ghost"
                                    onClick={() => {
                                      setRenamingId('');
                                      setDeviceName('');
                                    }}
                                    isDisabled={mutation.isPending}
                                  />
                                </>
                              ) : (
                                <Button
                                  label="Rename"
                                  variant="ghost"
                                  onClick={() => {
                                    setRenamingId(device.installation_id);
                                    setDeviceName(device.name || '');
                                  }}
                                  isDisabled={mutation.isPending}
                                />
                              )}
                              <Button
                                label={selected ? 'Selected target' : 'Select target'}
                                variant={selected ? 'secondary' : 'ghost'}
                                onClick={() =>
                                  run('/__hello_dev/target', {
                                    installation_id: device.installation_id,
                                  })
                                }
                                isDisabled={selected || mutation.isPending}
                              />
                              <Button
                                label="Forget"
                                variant="destructive"
                                onClick={() => forget(device)}
                                isDisabled={mutation.isPending}
                              />
                            </Stack>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              ) : (
                <EmptyState
                  title="No test devices enrolled"
                  description="Create a pairing QR to enroll the local Android test app."
                />
              )}
            </Stack>
          </Section>
          <Section padding={6}>
            <Stack gap={4}>
              <Heading level={2}>Send a test notification</Heading>
              <Text>
                {status.data.target
                  ? `Sending to ${status.data.target.name}.`
                  : 'Select a registered device before sending.'}
              </Text>
              <form onSubmit={(event) => void send(event)}>
                <FormLayout>
                  <TextInput
                    label="Notification title"
                    value={title}
                    onChange={(value) => setTitle(value.slice(0, 120))}
                    isRequired
                  />
                  <TextArea
                    label="Notification body"
                    value={body}
                    onChange={setBody}
                    maxLength={1000}
                    isRequired
                  />
                  <Button
                    label={
                      mutation.isPending && mutation.variables?.path === '/__hello_dev/send'
                        ? 'Sending test…'
                        : 'Send test push'
                    }
                    type="submit"
                    variant="primary"
                    isLoading={
                      mutation.isPending && mutation.variables?.path === '/__hello_dev/send'
                    }
                    isDisabled={
                      !status.data.target ||
                      mutation.isPending ||
                      sendUncertain ||
                      !title.trim() ||
                      !body.trim()
                    }
                  />
                </FormLayout>
              </form>
            </Stack>
          </Section>
          <Section padding={6}>
            <Stack gap={4}>
              <Heading level={2}>Latest test</Heading>
              {status.data.last_test ? (
                <Stack gap={2}>
                  <Text>
                    Campaign {status.data.last_test.campaign_id} ·{' '}
                    <Status value={status.data.last_test.status} />
                  </Text>
                  <Text>Campaign queued: {date(status.data.last_test.accepted_at)}</Text>
                  <Text>Provider accepted: {date(status.data.last_test.provider_accepted_at)}</Text>
                  <Text>
                    Observed on device:{' '}
                    {status.data.last_test.device_observed_at
                      ? date(status.data.last_test.device_observed_at)
                      : 'Check the notification on the test phone'}
                  </Text>
                  {status.data.last_test.reason && (
                    <Text>Reason: {status.data.last_test.reason}</Text>
                  )}
                  <Text type="supporting" color="secondary">
                    Provider acceptance confirms handoff to FCM. Device observation is separate and
                    appears only after a receipt arrives.
                  </Text>
                </Stack>
              ) : (
                <EmptyState
                  title="No test result recorded"
                  description="Send a test notification to begin collecting local receipt evidence."
                />
              )}
            </Stack>
          </Section>
        </>
      )}
    </PageFrame>
  );
}
