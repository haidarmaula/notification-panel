import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { CheckboxInput } from '@astryxdesign/core/CheckboxInput';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Section } from '@astryxdesign/core/Section';
import { Selector } from '@astryxdesign/core/Selector';
import { Table, proportional } from '@astryxdesign/core/Table';
import { Link } from '@astryxdesign/core/Link';
import { TextInput } from '@astryxdesign/core/TextInput';
import { data } from '../../lib/api/client';
import { Failure } from '../../components/ui/Feedback';
import type { components, operations } from '../../lib/api/generated/onboarding';

type Schemas = components['schemas'];
type CohortInput =
  operations['queueOnboardingTestCohort']['requestBody']['content']['application/json'];
type ReleaseInput =
  operations['queueDispatchRelease']['requestBody']['content']['application/json'];
type Targets =
  operations['listOnboardingCohortTargets']['responses'][200]['content']['application/json']['data'];
type Props = {
  applicationID: string;
  readiness: Schemas['Readiness'];
  approvals: Schemas['Approval'][];
  approvalError: string;
  operation?: Schemas['Operation'];
  cohortOperation?: Schemas['Operation'];
  locked: boolean;
  submitting?: 'activate' | 'cohort' | 'release';
  onActivate: (id: string) => void;
  onCohort: (body: CohortInput) => void;
  onRelease: (body: ReleaseInput) => void;
};
type Installation =
  operations['listOnboardingTestInstallations']['responses'][200]['content']['application/json']['data']['installations'][number];
type InstallationPage =
  operations['listOnboardingTestInstallations']['responses'][200]['content']['application/json']['data'];

function validApproval(approvals: Schemas['Approval'][], kind: Schemas['Approval']['kind']) {
  const now = Date.now();
  return approvals.filter(
    (item) => item.kind === kind && item.status === 'VALID' && Date.parse(item.expires_at) > now,
  );
}

function validReason(value: string) {
  const reason = value.trim();
  return (
    reason.length > 0 &&
    new TextEncoder().encode(reason).length <= 256 &&
    !Array.from(reason).some((character) => {
      const code = character.charCodeAt(0);
      return code < 32 || (code >= 127 && code <= 159);
    })
  );
}

export default function OnboardingLifecycle({
  applicationID,
  readiness,
  approvals,
  approvalError,
  operation,
  cohortOperation,
  locked,
  submitting,
  onActivate,
  onCohort,
  onRelease,
}: Props) {
  const base = `/admin/apps/${encodeURIComponent(applicationID)}/onboarding`;
  const active = readiness.application_status === 'ACTIVE';
  const [cursor, setCursor] = useState<string | null>(null);
  const [history, setHistory] = useState<(string | null)[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [cohortApproval, setCohortApproval] = useState('');
  const [pilotApproval, setPilotApproval] = useState('');
  const [fullApproval, setFullApproval] = useState('');
  const [reason, setReason] = useState('');

  const page = useQuery({
    queryKey: ['onboarding-test-installations', applicationID, cursor],
    queryFn: ({ signal }) =>
      data<InstallationPage>(
        `${base}/test-installations${cursor ? `?after=${encodeURIComponent(cursor)}` : ''}`,
        signal,
      ),
    enabled: active && !!readiness.current_configuration_id,
  });

  const latestTest =
    cohortOperation ??
    (operation?.kind === 'TEST_COHORT'
      ? operation
      : readiness.last_operation?.kind === 'TEST_COHORT'
        ? readiness.last_operation
        : undefined);
  const targets = useQuery({
    queryKey: [
      'onboarding-cohort-targets',
      applicationID,
      latestTest?.id,
      latestTest?.state,
      latestTest?.completed_at,
      latestTest?.resulting_revision,
    ],
    queryFn: ({ signal }) =>
      data<Targets>(`${base}/operations/${encodeURIComponent(latestTest!.id)}/targets`, signal),
    enabled: !!latestTest && latestTest.kind === 'TEST_COHORT',
    refetchInterval: latestTest && ['PENDING', 'RUNNING'].includes(latestTest.state) ? 3000 : false,
    refetchOnMount: 'always',
  });

  const testApprovals = validApproval(approvals, 'TEST_COHORT');
  const pilotApprovals = validApproval(approvals, 'PILOT');
  const fullApprovals = validApproval(approvals, 'FULL');
  const selectedCohortApproval = testApprovals.some((item) => item.id === cohortApproval);
  const verified = readiness.checks.some(
    (check) =>
      check.code === 'OAUTH' &&
      check.outcome === 'PASSED' &&
      !!readiness.candidate_configuration_id &&
      readiness.checks_configuration_id === readiness.candidate_configuration_id &&
      !!check.checked_at &&
      !!check.expires_at &&
      Date.parse(check.expires_at) > Date.now(),
  );
  const pending = !!operation && ['PENDING', 'RUNNING'].includes(operation.state);
  const canActivate =
    !locked &&
    !pending &&
    readiness.application_status === 'PROVISIONING' &&
    !!readiness.candidate_configuration_id &&
    !!readiness.enrollment_profile_id &&
    verified;
  const canCohort =
    active &&
    readiness.dispatch_mode === 'HELD' &&
    !approvalError &&
    !locked &&
    !pending &&
    !!readiness.current_configuration_id &&
    selected.length > 0 &&
    selected.length <= 10 &&
    selectedCohortApproval &&
    validReason(reason);

  const mode =
    readiness.dispatch_mode === 'HELD'
      ? 'PILOT'
      : readiness.dispatch_mode === 'PILOT'
        ? 'FULL'
        : undefined;
  const releaseOptions = mode === 'PILOT' ? pilotApprovals : mode === 'FULL' ? fullApprovals : [];
  const currentReleaseApproval = mode === 'PILOT' ? pilotApproval : fullApproval;
  const approvalIsCurrent = releaseOptions.some((item) => item.id === currentReleaseApproval);
  const releaseKind = mode === 'PILOT' ? 'RELEASE_PILOT' : 'RELEASE_FULL';
  const pendingSameModeRelease =
    !!mode && operation?.kind === releaseKind && operation.state === 'PENDING';
  const canRelease =
    active &&
    !approvalError &&
    !locked &&
    !!mode &&
    !!readiness.current_configuration_id &&
    !!readiness.cohort_id &&
    approvalIsCurrent &&
    (!pending || pendingSameModeRelease);

  function toggle(id: string, checked: boolean) {
    setSelected((current) => {
      if (checked) return current.includes(id) || current.length >= 10 ? current : [...current, id];
      return current.filter((value) => value !== id);
    });
  }

  function move(next: string | null) {
    setHistory((current) => [...current, cursor]);
    setCursor(next);
  }

  function selectApproval(kind: 'TEST_COHORT' | 'PILOT' | 'FULL', value: string) {
    if (kind === 'TEST_COHORT') setCohortApproval(value);
    if (kind === 'PILOT') setPilotApproval(value);
    if (kind === 'FULL') setFullApproval(value);
  }

  const previous = history.at(-1);
  const currentPage = page.data?.installations ?? [];
  const installationColumns = [
    {
      key: 'platform',
      header: 'Platform',
      width: proportional(1, { minWidth: 100 }),
      renderCell: (item: Installation) => item.platform,
    },
    {
      key: 'installation_id',
      header: 'Installation',
      width: proportional(3, { minWidth: 240 }),
    },
    {
      key: 'last_seen_at',
      header: 'Last seen',
      width: proportional(2, { minWidth: 160 }),
      renderCell: (item: Installation) =>
        item.last_seen_at ? new Date(item.last_seen_at).toLocaleString() : 'Not available',
    },
    {
      key: 'selection',
      header: 'Select',
      width: proportional(1, { minWidth: 120 }),
      renderCell: (item: Installation) => (
        <CheckboxInput
          label={`Select ${item.platform} installation ${item.installation_id}`}
          value={selected.includes(item.installation_id)}
          onChange={(checked) => toggle(item.installation_id, checked)}
          isDisabled={
            !active ||
            readiness.dispatch_mode !== 'HELD' ||
            locked ||
            pending ||
            (!selected.includes(item.installation_id) && selected.length >= 10)
          }
        />
      ),
    },
  ];

  return (
    <VStack gap={6}>
      <Section padding={6} dividers={['bottom']}>
        <VStack gap={4}>
          <Heading level={2}>Application lifecycle</Heading>
          <Text>
            An operator applies the activation request. When enrollment becomes active, dispatch
            stays held.
          </Text>
          <HStack gap={2} wrap="wrap" justify="start">
            <Button
              label="Activate enrollment"
              onClick={() =>
                readiness.candidate_configuration_id &&
                onActivate(readiness.candidate_configuration_id)
              }
              isDisabled={!canActivate}
              isLoading={submitting === 'activate'}
            />
          </HStack>
          {!verified && readiness.application_status === 'PROVISIONING' && (
            <Text color="secondary">
              A current OAuth pass for the candidate configuration and an enrollment profile are
              required before activation.
            </Text>
          )}
        </VStack>
      </Section>

      <Section padding={6} dividers={['bottom']}>
        <VStack gap={4}>
          <Heading level={2}>Test cohort dry-run</Heading>
          <Text>
            Choose up to ten eligible installations and use the operator approval for that exact
            roster.
          </Text>
          {!active && (
            <Banner
              status="info"
              title="Activation required"
              description="Activate enrollment before viewing eligible installations or queueing a device dry-run."
            />
          )}
          {active && readiness.dispatch_mode !== 'HELD' && (
            <Banner
              status="info"
              title="Dispatch is not held"
              description="The API accepts cohort dry-runs only while dispatch is HELD. Hold dispatch before preparing or replacing the test cohort."
            />
          )}
          {active && !readiness.current_configuration_id && (
            <Banner
              status="warning"
              title="Current configuration unavailable"
              description="Eligible installations are unavailable until the application has a current configuration."
            />
          )}
          {active && page.isError && (
            <Failure error={page.error} retry={() => void page.refetch()} />
          )}
          {active && page.isPending && <Text>Loading eligible installations…</Text>}
          {active && page.data && currentPage.length === 0 && (
            <Text>
              No eligible installations on this page. Enroll a test device and refresh status.
            </Text>
          )}
          {active && currentPage.length > 0 && (
            <Table
              aria-label="Eligible test installations"
              data={currentPage}
              idKey="installation_id"
              columns={installationColumns}
              density="balanced"
              hasHover
            />
          )}
          {active &&
            !page.isPending &&
            !page.isError &&
            page.data &&
            (currentPage.length > 0 || previous !== undefined) && (
              <HStack gap={2} wrap="wrap" justify="start">
                <Button
                  label="Previous"
                  onClick={() => {
                    setCursor(previous ?? null);
                    setHistory((current) => current.slice(0, -1));
                  }}
                  isDisabled={previous === undefined || locked || pending}
                />
                <Text>Selected: {selected.length} of 10</Text>
                <Button
                  label="Next"
                  onClick={() => page.data?.next_after && move(page.data.next_after)}
                  isDisabled={!page.data.next_after || locked || pending}
                />
              </HStack>
            )}
          {approvalError && (
            <Banner status="warning" title="Approvals unavailable" description={approvalError} />
          )}
          {active && readiness.dispatch_mode === 'HELD' && (
            <>
              <Selector
                label="Test cohort approval"
                value={cohortApproval}
                options={testApprovals.map((item) => ({
                  value: item.id,
                  label: `${item.id} · expires ${new Date(item.expires_at).toLocaleDateString()}`,
                }))}
                onChange={(value) =>
                  typeof value === 'string' && selectApproval('TEST_COHORT', value)
                }
                isDisabled={!active || locked || pending || testApprovals.length === 0}
              />
              <TextInput
                label="Dry-run reason"
                value={reason}
                onChange={setReason}
                isDisabled={!active || locked || pending}
              />
              {reason.trim() && !validReason(reason) && (
                <Text color="secondary">
                  Use a reason of at most 256 UTF-8 bytes without control characters.
                </Text>
              )}
              <Button
                label="Queue device dry-run"
                onClick={() =>
                  onCohort({
                    installation_ids: selected,
                    reason: reason.trim(),
                    evidence_id: cohortApproval,
                  })
                }
                isDisabled={!canCohort}
                isLoading={submitting === 'cohort'}
              />
            </>
          )}
          {latestTest && (
            <VStack gap={2}>
              <Heading level={3}>Per-device outcomes</Heading>
              <Text>
                Dry-run permission checks do not prove a delivered push or device receipt.
              </Text>
              {targets.isError ? (
                <Failure error={targets.error} retry={() => void targets.refetch()} />
              ) : targets.data?.length ? (
                <Table
                  aria-label="Device dry-run outcomes"
                  data={targets.data}
                  idKey="installation_id"
                  columns={[
                    {
                      key: 'platform',
                      header: 'Platform',
                      width: proportional(1, { minWidth: 100 }),
                    },
                    {
                      key: 'installation_id',
                      header: 'Installation',
                      width: proportional(3, { minWidth: 240 }),
                    },
                    {
                      key: 'outcome',
                      header: 'Outcome',
                      width: proportional(1, { minWidth: 120 }),
                      renderCell: (target) => target.outcome ?? 'Pending',
                    },
                    {
                      key: 'failure_code',
                      header: 'Detail',
                      width: proportional(2, { minWidth: 180 }),
                      renderCell: (target) => target.failure_code ?? '—',
                    },
                  ]}
                  density="balanced"
                  hasHover
                />
              ) : (
                <Text>
                  {latestTest.state === 'PENDING' || latestTest.state === 'RUNNING'
                    ? 'Waiting for device dry-run results.'
                    : 'There are no recorded outcomes for this operation.'}
                </Text>
              )}
            </VStack>
          )}
        </VStack>
      </Section>

      <Section padding={6}>
        <VStack gap={4}>
          <Heading level={2}>Request dispatch release</Heading>
          <Text>
            Requests queue protected operator work; dispatch changes only after it is applied. PILOT
            requires a current approved cohort. FULL additionally requires signed DELIVERED evidence
            for each configured platform and a separate FULL approval.
          </Text>
          {!active && (
            <Banner
              status="info"
              title="Activation required"
              description="Activate enrollment before requesting a dispatch release."
            />
          )}
          {readiness.dispatch_mode === 'FULL' ? (
            <Text>Dispatch is already in FULL mode.</Text>
          ) : (
            active &&
            mode && (
              <>
                <Heading level={3}>{mode} request</Heading>
                <Selector
                  label={`${mode} approval`}
                  value={currentReleaseApproval}
                  options={releaseOptions.map((item) => ({
                    value: item.id,
                    label: `${item.id} · expires ${new Date(item.expires_at).toLocaleDateString()}`,
                  }))}
                  onChange={(value) => typeof value === 'string' && selectApproval(mode, value)}
                  isDisabled={
                    !active ||
                    locked ||
                    (pending && !pendingSameModeRelease) ||
                    releaseOptions.length === 0
                  }
                />
                {pendingSameModeRelease && (
                  <Banner
                    status="warning"
                    title="Release operation is still pending"
                    description="If its delegated grant expired or was revoked, you may explicitly retry this same release mode with a valid approval. The server will supersede the old operation only when its grant is unusable; otherwise it will keep the mutation slot occupied."
                  />
                )}
                <Button
                  label={
                    pendingSameModeRelease
                      ? `Replace ${mode} release request`
                      : `Request ${mode} release`
                  }
                  onClick={() =>
                    readiness.cohort_id &&
                    onRelease({
                      mode,
                      cohort_id: readiness.cohort_id,
                      evidence_id: currentReleaseApproval,
                    })
                  }
                  isDisabled={!canRelease}
                  isLoading={submitting === 'release'}
                />
                {mode === 'FULL' && (
                  <Text color="secondary">
                    Create and send the approved PILOT campaign from the{' '}
                    <Link href={`/apps/${encodeURIComponent(applicationID)}/campaigns`}>
                      campaign workspace
                    </Link>
                    . Provider acceptance alone is not device delivery evidence. The protected
                    release executor remains authoritative for signed evidence on every configured
                    platform.
                  </Text>
                )}
              </>
            )
          )}
        </VStack>
      </Section>
    </VStack>
  );
}
