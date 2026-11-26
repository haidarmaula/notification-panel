import { useEffect, useEffectEvent, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { VStack, HStack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Section } from '@astryxdesign/core/Section';
import { Button } from '@astryxdesign/core/Button';
import { Banner } from '@astryxdesign/core/Banner';
import { TextInput } from '@astryxdesign/core/TextInput';
import { FileInput } from '@astryxdesign/core/FileInput';
import { CheckboxInput } from '@astryxdesign/core/CheckboxInput';
import { Selector } from '@astryxdesign/core/Selector';
import { MetadataList, MetadataListItem } from '@astryxdesign/core/MetadataList';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Token } from '@astryxdesign/core/Token';
import { Loading, Status } from '../../components/ui/Feedback';
import { PageFrame } from '../../components/ui/PageFrame';
import { SubmitAction } from '../../components/ui/Actions';
import { useAuth } from '../../app/auth-context';
import { useUnsaved } from '../../app/unsaved';
import { ApiError, request } from '../../lib/api/client';
import type { components, operations } from '../../lib/api/generated/onboarding';
import type { components as RegistryComponents } from '../../lib/api/generated/applications';
import OnboardingLifecycle from './OnboardingLifecycle';

type Readiness = components['schemas']['Readiness'];
type Profile = components['schemas']['Profile'];
type Approval = components['schemas']['Approval'];
type Operation = components['schemas']['Operation'];
type ServiceAccount = components['schemas']['ServiceAccount'];
type EnrollmentInput = components['schemas']['EnrollmentInput'];
type FcmInput = components['schemas']['FcmInput'];
type VerifyInput = components['schemas']['VerifyInput'];
type HoldInput = components['schemas']['HoldInput'];
type TestCohortInput =
  operations['queueOnboardingTestCohort']['requestBody']['content']['application/json'];
type ReleaseInput = components['schemas']['ReleaseInput'];
type Platform = EnrollmentInput['platforms'][number]['platform'];
type Environment = FcmInput['environment'];
type Application = RegistryComponents['schemas']['Application'];

type PendingMutation = {
  kind: 'enrollment' | 'credential' | 'verify' | 'hold' | 'activate' | 'cohort' | 'release';
  path: string;
  method: 'PUT' | 'POST';
  body: EnrollmentInput | FcmInput | VerifyInput | HoldInput | TestCohortInput | ReleaseInput;
  etag: string;
  key: string;
};

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function isRunning(operation: Operation) {
  return operation.state === 'PENDING' || operation.state === 'RUNNING';
}

const queuedOperationKinds = {
  verify: 'VERIFY',
  activate: 'ACTIVATE',
  cohort: 'TEST_COHORT',
} as const;

const writeNotices: Record<PendingMutation['kind'], string> = {
  enrollment: 'Enrollment profile saved.',
  credential: 'Credentials saved as a candidate.',
  verify: 'OAuth check accepted.',
  activate: 'Activation request queued.',
  cohort: 'Device dry-run queued.',
  release: 'Dispatch release request queued.',
  hold: 'Dispatch held.',
};

const operationLabels: Record<Operation['kind'], string> = {
  VERIFY: 'OAuth check',
  ACTIVATE: 'Enrollment activation',
  PROMOTE: 'Credential promotion',
  TEST_COHORT: 'Device dry-run',
  RELEASE_PILOT: 'PILOT release',
  RELEASE_FULL: 'FULL release',
};
function validText(value: string, maxBytes: number) {
  return (
    value.length > 0 &&
    new TextEncoder().encode(value).length <= maxBytes &&
    !Array.from(value).some((character) => {
      const code = character.charCodeAt(0);
      return code < 32 || (code >= 127 && code <= 159);
    })
  );
}
const allowedAccountKeys = new Set([
  'type',
  'project_id',
  'private_key_id',
  'private_key',
  'client_email',
  'token_uri',
  'auth_uri',
  'client_id',
  'auth_provider_x509_cert_url',
  'client_x509_cert_url',
  'universe_domain',
]);

function safeError(error: unknown): string {
  const status = error instanceof ApiError ? error.status : 0;
  if (status === 400) return 'Invalid input. Check the selected options and field formats.';
  if (status === 401) return 'Your session has expired. Sign in to continue.';
  if (status === 403) return 'You do not have access to set up this application.';
  if (status === 404) return 'Setup is unavailable for this application.';
  if (status === 409)
    return 'The request conflicts with current setup. Review the latest status and approvals.';
  if (status === 412)
    return 'Setup has changed. Reload and review the latest version before submitting again.';
  if (status === 428) return 'The resource version is unavailable. Reload before trying again.';
  if (status === 429) return 'Too many requests. Wait before trying again.';
  if (status === 503) return 'Setup is unavailable. Check the status before trying again.';
  if (status === 0) return 'The outcome is unknown. Retry the same request to recover its result.';
  if (status >= 500) return 'The service is unavailable. Check the latest status before retrying.';
  return 'The request could not be processed.';
}

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isFinite(date.getTime())
    ? new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
    : 'Time unavailable';
}

function blockerLabel(code: string) {
  const labels: Record<string, string> = {
    FCM_NOT_CONFIGURED: 'Firebase credentials have not been saved.',
    FCM_NOT_VERIFIED: 'Firebase credentials have not been checked.',
    VERIFIER_NOT_CONFIGURED: 'The OAuth verifier is unavailable. Status will be checked again.',
    FCM_CREDENTIAL_REJECTED: 'The service account credentials were rejected.',
    FCM_CHECK_INCONCLUSIVE: 'The OAuth check is inconclusive.',
    FCM_CONFIGURATION_UNAVAILABLE: 'The service account configuration is unavailable.',
    ENROLLMENT_NOT_CONFIGURED: 'An enrollment profile has not been selected.',
    RUNTIME_PENDING: 'Device enrollment has not been enabled.',
    LEGACY_ADOPTION_REQUIRED:
      'Platform operators must review the existing application configuration.',
    PLATFORM_HOLD: 'Platform setup is held.',
    TEST_DEVICE_REQUIRED: 'A test device has not been configured.',
    ROUTE_REVIEW_REQUIRED: 'Device routes have not been reviewed.',
    DISPATCH_APPROVAL_REQUIRED: 'Dispatch approval is not available.',
  };
  return labels[code] ?? code;
}

function checkLabel(code: string) {
  const labels: Record<string, string> = {
    LOCAL_FORMAT: 'Credential format',
    OAUTH: 'OAuth connection',
    TARGET_PERMISSION: 'Delivery permission',
    DEVICE_DELIVERY: 'Device receipt',
  };
  return labels[code] ?? code;
}

function outcomeLabel(value: string) {
  if (value === 'PASSED') return 'Passed';
  if (value === 'FAILED') return 'Failed';
  if (value === 'INCONCLUSIVE') return 'Inconclusive';
  return 'Not checked';
}

function operationMessage(operation: Operation) {
  if (operation.kind !== 'VERIFY') {
    const label = operationLabels[operation.kind] ?? 'Setup operation';
    if (operation.state === 'SUCCEEDED') return `${label} succeeded.`;
    if (operation.state === 'FAILED') return `${label} failed.`;
    if (operation.state === 'SUPERSEDED') return `${label} was superseded.`;
    if (operation.kind === 'ACTIVATE' && operation.state === 'PENDING')
      return 'Enrollment activation is awaiting operator action.';
    if (operation.kind.startsWith('RELEASE_') && operation.state === 'PENDING')
      return `${label} is awaiting operator action.`;
    return `${label} is ${operation.state === 'PENDING' ? 'queued' : 'running'}.`;
  }
  if (operation.state === 'SUCCEEDED')
    return 'OAuth succeeded. Delivery permissions and device receipt have not been checked.';
  if (operation.failure_code === 'FCM_CREDENTIAL_REJECTED')
    return 'The provider rejected the service account. Check the credentials before trying again.';
  if (operation.failure_code === 'FCM_CHECK_INCONCLUSIVE')
    return 'The OAuth check is inconclusive. You can submit a new check.';
  if (operation.failure_code === 'FCM_CONFIGURATION_UNAVAILABLE')
    return 'The service account configuration is unavailable for verification.';
  if (operation.state === 'SUPERSEDED')
    return 'The previous check was superseded. Reload setup status.';
  if (operation.state === 'FAILED')
    return 'The OAuth check failed. Check the configuration before trying again.';
  return operation.state === 'PENDING'
    ? 'The OAuth check is queued.'
    : 'The OAuth check is running.';
}

function strongETag(value: string | null): value is string {
  return !!value && /^"[^"\r\n]+"$/.test(value);
}

function serviceAccount(value: unknown): value is ServiceAccount {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const account = value as Record<string, unknown>;
  return (
    Object.keys(account).every((key) => allowedAccountKeys.has(key)) &&
    account.type === 'service_account' &&
    typeof account.project_id === 'string' &&
    account.project_id.length > 0 &&
    account.project_id.length <= 128 &&
    typeof account.private_key_id === 'string' &&
    account.private_key_id.length > 0 &&
    typeof account.private_key === 'string' &&
    account.private_key.length > 0 &&
    typeof account.client_email === 'string' &&
    account.client_email.length > 0 &&
    account.token_uri === 'https://oauth2.googleapis.com/token' &&
    (account.universe_domain === undefined || account.universe_domain === 'googleapis.com')
  );
}

export default function Onboarding(props: { applicationID: string }) {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const setDirty = useUnsaved();
  const applicationIDValid = () => uuidPattern.test(props.applicationID);
  const base = () => `/admin/apps/${encodeURIComponent(props.applicationID)}/onboarding`;
  const [readiness, setReadiness] = useState<Readiness>();
  const [application, setApplication] = useState<Pick<Application, 'id' | 'name' | 'key'>>();
  const [applicationError, setApplicationError] = useState('');
  const [etag, setEtag] = useState('');
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [profileError, setProfileError] = useState('');
  const [approvalError, setApprovalError] = useState('');
  const [writeError, setWriteError] = useState('');
  const [writeNotice, setWriteNotice] = useState('');
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [activeMutationKind, setActiveMutationKind] = useState<PendingMutation['kind'] | null>(
    null,
  );
  const [pendingMutation, setPendingMutation] = useState<PendingMutation>();
  const [retryAfter, setRetryAfter] = useState('');
  const [retryBlocked, setRetryBlocked] = useState(false);
  const [profileID, setProfileID] = useState('');
  const [editingProfile, setEditingProfile] = useState(false);
  const [profileDirty, setProfileDirty] = useState(false);
  const [selectedPlatforms, setSelectedPlatforms] = useState<Platform[]>([]);
  const [identifiers, setIdentifiers] = useState<Record<Platform, string>>({
    ANDROID: '',
    IOS: '',
  });
  const [environment, setEnvironment] = useState<Environment | ''>('');
  const [account, setAccount] = useState<ServiceAccount>();
  const [fileError, setFileError] = useState('');
  const [holdReason, setHoldReason] = useState('');
  const [operation, setOperation] = useState<Operation>();
  const [cohortOperation, setCohortOperation] = useState<Operation>();
  const [operationError, setOperationError] = useState('');
  const [settlingOperation, setSettlingOperation] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const [credentialFile, setCredentialFile] = useState<File | null>(null);
  const credentialSelection = useRef(0);
  const alive = useRef(true);
  const readinessTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const operationTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const observedOperation = useRef('');
  const readinessRequest = useRef(0);
  const lifetime = useRef(new AbortController());
  const mutationInFlight = useRef(false);
  useEffect(() => {
    setDirty(profileDirty || !!account || !!pendingMutation || busy || !!holdReason.trim());
  }, [profileDirty, account, pendingMutation, busy, holdReason, setDirty]);
  useEffect(() => {
    if (!retryBlocked) return;
    const timer = setTimeout(
      () => {
        setRetryBlocked(false);
        setRetryAfter('');
      },
      Number(retryAfter) * 1000,
    );
    return () => clearTimeout(timer);
  }, [retryAfter, retryBlocked]);

  function clearTimers() {
    if (readinessTimer.current) clearTimeout(readinessTimer.current);
    if (operationTimer.current) clearTimeout(operationTimer.current);
    readinessTimer.current = undefined;
    operationTimer.current = undefined;
  }

  function clearCredential() {
    credentialSelection.current++;
    setAccount(undefined);
    setFileError('');
    setCredentialFile(null);
    if (fileInput.current) fileInput.current.value = '';
  }

  function handleUnauthorized(error: unknown) {
    if (error instanceof ApiError && error.status === 401)
      window.dispatchEvent(new Event('hello:session-expired'));
  }

  async function readReadiness(showLoading = false): Promise<Readiness | undefined> {
    const signal = lifetime.current.signal;
    const sequence = ++readinessRequest.current;
    if (showLoading) setRefreshing(true);
    setLoadError('');
    try {
      let responseETag: string | null = null;
      const envelope = await request<components['schemas']['ReadinessEnvelope']>(
        `/bff/api${base()}`,
        {
          signal,
          onHeaders: (headers) => {
            responseETag = headers.get('ETag');
          },
        },
      );
      if (!strongETag(responseETag) || envelope.data.application_id !== props.applicationID)
        throw new ApiError(502, 'Invalid application context');
      if (!alive.current || signal.aborted || sequence !== readinessRequest.current)
        return undefined;
      setReadiness(envelope.data);
      if (envelope.data.environment) setEnvironment(envelope.data.environment);
      setEtag(responseETag);
      if (readinessTimer.current) clearTimeout(readinessTimer.current);
      readinessTimer.current = undefined;
      if (envelope.data.blockers.includes('VERIFIER_NOT_CONFIGURED')) {
        readinessTimer.current = setTimeout(() => void readReadiness(), 2000);
      }
      return envelope.data;
    } catch (error) {
      if (!alive.current || signal.aborted || sequence !== readinessRequest.current)
        return undefined;
      handleUnauthorized(error);
      setLoadError(safeError(error));
      return undefined;
    } finally {
      if (alive.current && !signal.aborted && sequence === readinessRequest.current) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  }

  async function readProfiles() {
    const signal = lifetime.current.signal;
    setProfileError('');
    try {
      const response = await request<components['schemas']['ProfileListEnvelope']>(
        `/bff/api${base()}/enrollment-profiles`,
        { signal },
      );
      if (alive.current && !signal.aborted) setProfiles(response.data.items);
    } catch (error) {
      if (!alive.current || signal.aborted) return;
      handleUnauthorized(error);
      setProfileError(
        error instanceof ApiError && error.status === 404
          ? 'Enrollment profiles are unavailable for this application.'
          : safeError(error),
      );
    }
  }

  async function readApprovals() {
    const signal = lifetime.current.signal;
    setApprovalError('');
    try {
      const response = await request<components['schemas']['ApprovalListEnvelope']>(
        `/bff/api${base()}/approvals`,
        { signal },
      );
      if (alive.current && !signal.aborted) setApprovals(response.data.items);
    } catch (error) {
      if (!alive.current || signal.aborted) return;
      handleUnauthorized(error);
      setApprovalError(
        error instanceof ApiError && error.status === 404
          ? 'Approvals are unavailable for this application.'
          : safeError(error),
      );
    }
  }

  async function readApplication() {
    const signal = lifetime.current.signal;
    try {
      const result = await request<{ data: Application }>(
        `/bff/api/admin/apps/${encodeURIComponent(props.applicationID)}`,
        { signal },
      );
      if (!alive.current || signal.aborted) return;
      if (result.data.id !== props.applicationID)
        throw new ApiError(502, 'Invalid application context');
      setApplication({ id: result.data.id, name: result.data.name, key: result.data.key });
      setApplicationError('');
    } catch (error) {
      if (!alive.current || signal.aborted) return;
      setApplication(undefined);
      handleUnauthorized(error);
      setApplicationError(safeError(error));
    }
  }

  async function refreshAll() {
    if (!applicationIDValid()) {
      setLoadError(
        'Invalid application ID. Return to the registry and select the application again.',
      );
      setLoading(false);
      return;
    }
    setLoading(true);
    const [current] = await Promise.all([
      readReadiness(),
      readApplication(),
      readProfiles(),
      readApprovals(),
      queryClient.invalidateQueries({
        queryKey: ['onboarding-test-installations', props.applicationID],
      }),
      queryClient.invalidateQueries({
        queryKey: ['onboarding-cohort-targets', props.applicationID],
      }),
    ]);
    if (current?.last_operation) observeOperation(current.last_operation);
  }

  async function refreshReadinessForReview() {
    const [result] = await Promise.all([readReadiness(true), readApprovals()]);
    if (result) {
      if (result.last_operation) observeOperation(result.last_operation);
      setConflict(false);
      setWriteNotice('Latest status loaded. Review the revision and next steps.');
    }
  }

  function makePending(
    kind: PendingMutation['kind'],
    suffix: string,
    method: PendingMutation['method'],
    body: PendingMutation['body'],
  ): PendingMutation | undefined {
    const version = etag;
    if (
      !version ||
      conflict ||
      pendingMutation ||
      mutationInFlight.current ||
      settlingOperation ||
      !!loadError ||
      !application ||
      !uuidPattern.test(props.applicationID)
    ) {
      setWriteError('Reload setup status before submitting changes.');
      return undefined;
    }
    return {
      kind,
      path: `${base()}${suffix}`,
      method,
      body,
      etag: version,
      key: crypto.randomUUID(),
    };
  }

  async function runMutation(mutation: PendingMutation) {
    if (mutationInFlight.current || retryBlocked) return;
    mutationInFlight.current = true;
    setActiveMutationKind(mutation.kind);
    setBusy(true);
    setWriteError('');
    setWriteNotice('');
    setRetryAfter('');
    let responseETag: string | null = null;
    let replayed = false;
    let retryHeader = '';
    let responseLocation = '';
    try {
      const response = await request<unknown>(`/bff/api${mutation.path}`, {
        method: mutation.method,
        body: mutation.body,
        csrf: auth.csrf(),
        idempotencyKey: mutation.key,
        ifMatch: mutation.etag,
        signal: lifetime.current.signal,
        onHeaders: (headers) => {
          responseETag = headers.get('ETag');
          replayed = headers.get('Idempotency-Replayed') === 'true';
          responseLocation = headers.get('Location') ?? '';
          const retry = headers.get('Retry-After') ?? '';
          if (/^[1-9][0-9]{0,3}$/.test(retry) && Number(retry) <= 3600) retryHeader = retry;
        },
      });
      if (!alive.current) return;
      setPendingMutation(undefined);
      setConflict(false);
      if (!replayed && strongETag(responseETag)) setEtag(responseETag);
      if (mutation.kind === 'credential') clearCredential();
      if (mutation.kind === 'enrollment') {
        setEditingProfile(false);
        setProfileDirty(false);
      }
      if (mutation.kind === 'hold') setHoldReason('');
      setWriteNotice(
        replayed ? 'The previous request has already been applied.' : writeNotices[mutation.kind],
      );
      const expectedKind =
        mutation.kind === 'release' && 'mode' in mutation.body
          ? `RELEASE_${mutation.body.mode}`
          : mutation.kind in queuedOperationKinds
            ? queuedOperationKinds[mutation.kind as keyof typeof queuedOperationKinds]
            : undefined;
      if (expectedKind) {
        const envelope = response as components['schemas']['OperationEnvelope'];
        const accepted = envelope?.data;
        const expectedLocation = `/bff/api${base()}/operations/${accepted?.id}`;
        if (
          !accepted ||
          accepted.kind !== expectedKind ||
          !uuidPattern.test(accepted.id) ||
          responseLocation !== expectedLocation
        ) {
          setOperationError(
            'The operation location is invalid. Refresh status to recover the request.',
          );
        } else {
          observeOperation(accepted, true);
        }
      }
      await Promise.all([readReadiness(), readApprovals()]);
    } catch (error) {
      if (!alive.current) return;
      handleUnauthorized(error);
      const status = error instanceof ApiError ? error.status : 0;
      if (status === 412) setConflict(true);
      setWriteError(safeError(error));
      if (retryHeader && (status === 429 || status === 503)) {
        setRetryAfter(retryHeader);
        setRetryBlocked(true);
      }
      if (status === 0 || status === 429 || status === 503 || status >= 500) {
        setPendingMutation(mutation);
      } else {
        setPendingMutation(undefined);
        if (mutation.kind === 'credential') clearCredential();
      }
      if (status === 409) await refreshAll();
    } finally {
      mutationInFlight.current = false;
      if (alive.current) {
        setBusy(false);
        setActiveMutationKind(null);
      }
    }
  }

  function recordOperation(value: Operation) {
    setOperation(value);
    if (value.kind === 'TEST_COHORT') setCohortOperation(value);
  }

  function observeOperation(value: Operation, force = false) {
    if (!alive.current || !uuidPattern.test(value.id)) return;
    if (observedOperation.current === value.id && !force) return;
    if (operationTimer.current) clearTimeout(operationTimer.current);
    observedOperation.current = value.id;
    recordOperation(value);
    setOperationError('');
    if (force || isRunning(value)) void pollOperation(value.id, value.kind);
  }

  async function pollOperation(operationID: string, kind: Operation['kind']) {
    const signal = lifetime.current.signal;
    const current = () =>
      alive.current && !signal.aborted && observedOperation.current === operationID;
    if (!current()) return;
    let retryDelay = 2000;
    try {
      const response = await request<components['schemas']['OperationEnvelope']>(
        `/bff/api${base()}/operations/${operationID}`,
        {
          signal,
          onHeaders: (headers) => {
            const seconds = Number(headers.get('Retry-After'));
            if (Number.isInteger(seconds) && seconds > 0 && seconds <= 3600)
              retryDelay = seconds * 1000;
          },
        },
      );
      if (!current()) return;
      if (response.data.id !== operationID || response.data.kind !== kind)
        throw new ApiError(502, 'Invalid operation context');
      recordOperation(response.data);
      setOperationError('');
      if (isRunning(response.data)) {
        operationTimer.current = setTimeout(() => void pollOperation(operationID, kind), 2000);
      } else {
        operationTimer.current = undefined;
        setSettlingOperation(true);
        await Promise.all([readReadiness(), readApprovals()]);
        if (current()) setSettlingOperation(false);
      }
    } catch (error) {
      if (!current()) return;
      handleUnauthorized(error);
      setOperationError(safeError(error));
      const status = error instanceof ApiError ? error.status : 0;
      if (status === 0 || status === 429 || status >= 500) {
        operationTimer.current = setTimeout(
          () => void pollOperation(operationID, kind),
          retryDelay,
        );
      } else {
        observedOperation.current = '';
      }
    }
  }

  function selectProfile(value: string) {
    setProfileDirty(true);
    setProfileID(value);
    const profile = profiles.find((item) => item.id === value);
    setSelectedPlatforms(profile?.platforms.slice(0, 1) ?? []);
  }

  function togglePlatform(platform: Platform, checked: boolean) {
    setSelectedPlatforms((current) => {
      if (checked) return current.includes(platform) ? current : [...current, platform];
      return current.filter((value) => value !== platform);
    });
  }

  function editIdentifier(platform: Platform, value: string) {
    setIdentifiers((current) => ({ ...current, [platform]: value }));
  }

  async function saveEnrollment(event: FormEvent) {
    event.preventDefault();
    setWriteError('');
    const profile = profiles.find((item) => item.id === profileID);
    const platforms = profile?.platforms.filter((value) => selectedPlatforms.includes(value)) ?? [];
    if (
      !profile ||
      platforms.length < 1 ||
      platforms.some((value) => {
        const identifier = identifiers[value].trim();
        return !validText(identifier, 255);
      })
    ) {
      setWriteError('Select a profile, at least one platform, and valid application identifiers.');
      return;
    }
    const body: EnrollmentInput = {
      profile_id: profile.id,
      platforms: platforms.map((platform) => ({
        platform,
        application_identifier: identifiers[platform].trim(),
      })),
    };
    const mutation = makePending('enrollment', '/enrollment', 'PUT', body);
    if (mutation) await runMutation(mutation);
  }

  async function chooseCredential(files: File | File[] | null) {
    if (busy || conflict || pendingMutation) return;
    const file = Array.isArray(files) ? files[0] : files;
    setCredentialFile(file ?? null);
    const selection = ++credentialSelection.current;
    setAccount(undefined);
    setFileError('');
    setWriteError('');
    if (!file) return;
    if (file.size > 64 * 1024) {
      clearCredential();
      setFileError('The JSON file must not exceed 64 KiB.');
      return;
    }
    try {
      const parsed: unknown = JSON.parse(await file.text());
      if (!alive.current || selection !== credentialSelection.current) return;
      if (!serviceAccount(parsed)) throw new Error('invalid account');
      setAccount(parsed);
    } catch {
      if (!alive.current || selection !== credentialSelection.current) return;
      clearCredential();
      setFileError('The file is not a supported service account. Choose another JSON file.');
    }
  }

  async function storeCredential(event: FormEvent) {
    event.preventDefault();
    setWriteError('');
    const value = account;
    if (!value || !environment) {
      setFileError('Select an environment and credential file first.');
      return;
    }
    const body: FcmInput = {
      environment: environment as Environment,
      target_project_id: value.project_id,
      service_account: value,
    };
    const mutation = makePending('credential', '/fcm', 'PUT', body);
    if (mutation) await runMutation(mutation);
  }

  async function verifyCredential() {
    const candidate = readiness?.candidate_configuration_id;
    if (!candidate || readiness?.blockers.includes('VERIFIER_NOT_CONFIGURED')) return;
    const body: VerifyInput = { configuration_id: candidate };
    const mutation = makePending('verify', '/fcm/verify', 'POST', body);
    if (mutation) await runMutation(mutation);
  }

  async function holdDispatch(event: FormEvent) {
    event.preventDefault();
    const reason = holdReason.trim();
    if (!validText(reason, 256)) {
      setWriteError('Enter a hold reason of at most 256 UTF-8 bytes.');
      return;
    }
    const body: HoldInput = { reason };
    const mutation = makePending('hold', '/dispatch/hold', 'POST', body);
    if (mutation) await runMutation(mutation);
  }

  function submitLifecycle(
    kind: 'activate' | 'cohort' | 'release',
    body: { configuration_id: string } | TestCohortInput | ReleaseInput,
  ) {
    const routes = {
      activate: { suffix: '/activate', method: 'POST' },
      cohort: { suffix: '/test-cohort', method: 'PUT' },
      release: { suffix: '/dispatch/release', method: 'POST' },
    } as const;
    const { suffix, method } = routes[kind];
    const mutation = makePending(kind, suffix, method, body);
    if (mutation) void runMutation(mutation);
  }

  const initialize = useEffectEvent(() => {
    void refreshAll();
  });
  useEffect(() => {
    alive.current = true;
    observedOperation.current = '';
    const selection = credentialSelection;
    const controller = new AbortController();
    lifetime.current = controller;
    initialize();
    return () => {
      alive.current = false;
      selection.current++;
      clearTimers();
      controller.abort();
    };
  }, []);

  const selectedProfile = () => profiles.find((item) => item.id === profileID);
  const verifierReady = () => !readiness?.blockers.includes('VERIFIER_NOT_CONFIGURED');
  const credentialReady = () => !!account && !!environment && !!etag && !conflict && !busy;

  const locked =
    busy ||
    conflict ||
    !!pendingMutation ||
    !application ||
    !!loadError ||
    refreshing ||
    loading ||
    settlingOperation;
  return (
    <PageFrame
      title="Notification setup"
      description="Set up device enrollment, verify delivery readiness, and request dispatch release."
      back={{
        label: 'Back to application registry',
        href: `/apps/admin/applications/${props.applicationID}`,
      }}
      actions={
        <Button
          label={refreshing ? 'Refreshing…' : 'Refresh status'}
          onClick={() => void refreshAll()}
          isDisabled={refreshing || busy}
        />
      }
      contentWidth={1120}
    >
      {loadError && (
        <Banner
          status="error" role="alert"
          title="Unable to load setup"
          description={loadError}
          endContent={<Button label="Retry loading" onClick={() => void refreshAll()} />}
        />
      )}
      {applicationError && (
        <Banner
          status="error" role="alert"
          title="Application context unavailable"
          description={applicationError}
        />
      )}
      {loading && <Loading label="Loading setup status…" />}
      {readiness && (
        <>
          <Section padding={6} dividers={['bottom']}>
            <VStack gap={4}>
              <VStack gap={2}>
                <Heading level={2}>Application status</Heading>
                {application && (
                  <Text weight="semibold" wordBreak="break-word">
                    {application.name} · {application.key}
                  </Text>
                )}
              </VStack>
              <MetadataList>
                <MetadataListItem label="Application ID">
                  <Text wordBreak="break-word">{props.applicationID}</Text>
                </MetadataListItem>
                <MetadataListItem label="Enrollment status">
                  <Status value={readiness.application_status} />
                </MetadataListItem>
                <MetadataListItem label="Dispatch mode">
                  <Token
                    label={dispatchLabel(readiness.dispatch_mode)}
                    color={readiness.dispatch_mode === 'HELD' ? 'gray' : 'blue'}
                    size="sm"
                  />
                </MetadataListItem>
                <MetadataListItem label="Setup revision">{readiness.revision}</MetadataListItem>
                <MetadataListItem label="Environment">
                  {readiness.environment ?? 'Not selected'}
                </MetadataListItem>
                {readiness.candidate_configuration_id && (
                  <MetadataListItem label="Credential candidate">
                    <Text wordBreak="break-word">{readiness.candidate_configuration_id}</Text>
                  </MetadataListItem>
                )}
                {readiness.current_configuration_id && (
                  <MetadataListItem label="Active credential">
                    <Text wordBreak="break-word">{readiness.current_configuration_id}</Text>
                  </MetadataListItem>
                )}
              </MetadataList>
              {!!readiness.blockers.length && (
                <VStack gap={2}>
                  <Heading level={3}>Remaining requirements</Heading>
                  {readiness.blockers.map((code) => (
                    <Text key={code}>{blockerLabel(code)}</Text>
                  ))}
                </VStack>
              )}
              {!!readiness.checks.length && (
                <VStack gap={2}>
                  <Heading level={3}>Checks</Heading>
                  {readiness.checks_configuration_id && (
                    <Text color="secondary" wordBreak="break-word">
                      Evidence for credential {readiness.checks_configuration_id}
                    </Text>
                  )}
                  {readiness.checks.map((check) => (
                    <Text key={check.code}>
                      {checkLabel(check.code)}: {outcomeLabel(check.outcome)}
                    </Text>
                  ))}
                </VStack>
              )}
            </VStack>
          </Section>
          <Section padding={6} dividers={['bottom']}>
            <VStack gap={4}>
              <Heading level={2}>Enrollment profile</Heading>
              {readiness.enrollment_profile_id && (
                <Text>
                  Saved profile:{' '}
                  {profiles.find((profile) => profile.id === readiness.enrollment_profile_id)
                    ?.name ?? readiness.enrollment_profile_id}
                </Text>
              )}
              <Text color="secondary">
                Choose the application's enrollment profile and device package or bundle
                identifiers.
              </Text>
              {profileError && (
                <Banner status="error" role="alert" title="Profiles unavailable" description={profileError} />
              )}
              {!readiness.enrollment_profile_id || editingProfile ? (
                <VStack
                  as="form"
                  gap={4}
                  maxWidth={720}
                  width="100%"
                  onSubmit={(event) => void saveEnrollment(event)}
                >
                  <FormLayout>
                    <Selector
                      label="Enrollment profile"
                      value={profileID}
                      options={profiles.map((profile) => ({
                        value: profile.id,
                        label: profile.name,
                      }))}
                      onChange={(value) => {
                        if (typeof value === 'string') selectProfile(value);
                      }}
                      isDisabled={locked || !profiles.length}
                    />
                    {selectedProfile() && (
                      <VStack gap={3}>
                        <Heading level={3}>Platforms</Heading>
                        {selectedProfile()!.platforms.map((platform) => (
                          <CheckboxInput
                            key={platform}
                            label={platform === 'ANDROID' ? 'Android' : 'iOS'}
                            value={selectedPlatforms.includes(platform)}
                            onChange={(checked) => togglePlatform(platform, checked)}
                            isDisabled={locked}
                          />
                        ))}
                      </VStack>
                    )}
                    {selectedPlatforms.map((platform) => (
                      <TextInput
                        key={platform}
                        label={platform === 'ANDROID' ? 'Android application ID' : 'iOS bundle ID'}
                        value={identifiers[platform]}
                        onChange={(value) => editIdentifier(platform, value)}
                        autoComplete="off"
                        isDisabled={locked}
                      />
                    ))}
                    <HStack gap={2} wrap="wrap" justify="start">
                      <SubmitAction
                        label="Save profile"
                        isLoading={busy && activeMutationKind === 'enrollment'}
                        isDisabled={locked || !etag}
                      />
                      {readiness.enrollment_profile_id && (
                        <Button
                          label="Cancel profile changes"
                          onClick={() => {
                            setEditingProfile(false);
                            setProfileDirty(false);
                          }}
                          isDisabled={busy}
                        />
                      )}
                    </HStack>
                  </FormLayout>
                </VStack>
              ) : (
                <HStack>
                  <Button
                    label="Change enrollment profile"
                    onClick={() => {
                      selectProfile(readiness.enrollment_profile_id ?? '');
                      setEditingProfile(true);
                    }}
                    isDisabled={locked}
                  />
                </HStack>
              )}
            </VStack>
          </Section>
          <Section padding={6} dividers={['bottom']}>
            <VStack gap={4}>
              <Heading level={2}>Firebase credentials</Heading>
              <Text color="secondary">
                The file is used only for this request. Private keys are never displayed or saved in
                browser storage.
              </Text>
              <VStack
                as="form"
                gap={4}
                maxWidth={720}
                width="100%"
                onSubmit={(event) => void storeCredential(event)}
              >
                <FormLayout>
                  <Selector
                    label="Environment"
                    value={environment}
                    options={[
                      { value: 'staging', label: 'Staging' },
                      { value: 'production', label: 'Production' },
                    ]}
                    onChange={(value) => {
                      if (value === 'staging' || value === 'production') setEnvironment(value);
                    }}
                    isDisabled={locked || !!readiness.environment}
                  />
                  <FileInput
                    ref={fileInput}
                    label="FCM credential file (service account JSON)"
                    value={credentialFile}
                    onChange={(files) => void chooseCredential(files)}
                    accept="application/json,.json"
                    description="Maximum 64 KiB. Use a service account from the selected Firebase project."
                    isDisabled={locked}
                  />
                  {fileError && (
                    <Banner
                      status="error" role="alert"
                      title="Invalid credential file"
                      description={fileError}
                    />
                  )}
                  {account && <Text>Detected project: {account.project_id}</Text>}
                  <HStack gap={2} wrap="wrap" justify="start">
                    <SubmitAction
                      label="Save credentials"
                      isLoading={busy && activeMutationKind === 'credential'}
                      isDisabled={locked || !credentialReady()}
                    />
                  </HStack>
                </FormLayout>
              </VStack>
            </VStack>
          </Section>
          <Section padding={6} dividers={['bottom']}>
            <VStack gap={4}>
              <Heading level={2}>OAuth verification</Heading>
              <Text color="secondary">
                This check requests an OAuth token. It does not send notifications or verify
                delivery permissions or device receipt.
              </Text>
              {readiness.candidate_configuration_id ? (
                <HStack>
                  <Button
                    label="Verify FCM"
                    variant="primary"
                    isLoading={busy && activeMutationKind === 'verify'}
                    onClick={() => void verifyCredential()}
                    isDisabled={
                      locked ||
                      !etag ||
                      !verifierReady() ||
                      (!!operation && ['PENDING', 'RUNNING'].includes(operation.state))
                    }
                  />
                </HStack>
              ) : (
                <Text>Save a credential candidate before running this check.</Text>
              )}
              {!verifierReady() && (
                <Text role="status">
                  The verifier is unavailable. Status will be checked again automatically.
                </Text>
              )}
            </VStack>
          </Section>
          {(operation ||
            operationError ||
            writeError ||
            pendingMutation ||
            conflict ||
            writeNotice ||
            retryAfter) && (
            <Section padding={6} dividers={['bottom']}>
              <VStack gap={4}>
                <Heading level={2}>Setup activity</Heading>
                {operation && (
                  <Banner
                    status={operation.state === 'FAILED' ? 'error' : 'info'}
                    title={operationMessage(operation)}
                    description={`Status: ${operationState(operation.state)}.${operation.failure_code ? ` Code: ${operation.failure_code}.` : ''}`}
                  />
                )}
                {operationError && (
                  <Banner
                    status="error" role="alert"
                    title="Operation status unavailable"
                    description={operationError}
                  />
                )}
                {writeError && (
                  <Banner status="error" role="alert" title="Request not completed" description={writeError} />
                )}
                {retryAfter && <Text>Wait {retryAfter} seconds before trying again.</Text>}
                {pendingMutation && (
                  <HStack>
                    <Button
                      label="Retry the same request"
                      onClick={() => void runMutation(pendingMutation)}
                      isLoading={busy && activeMutationKind === pendingMutation.kind}
                      isDisabled={busy || retryBlocked}
                    />
                  </HStack>
                )}
                {conflict && (
                  <Banner
                    status="warning"
                    title="Setup revision changed"
                    description="Review the latest status before submitting new changes."
                    endContent={
                      <Button
                        label="Reload and review"
                        onClick={() => void refreshReadinessForReview()}
                        isDisabled={refreshing}
                      />
                    }
                  />
                )}
                {writeNotice && <Banner status="info" title={writeNotice} />}
              </VStack>
            </Section>
          )}
          <OnboardingLifecycle
            applicationID={props.applicationID}
            readiness={readiness}
            approvals={approvals}
            approvalError={approvalError}
            operation={operation}
            cohortOperation={cohortOperation}
            locked={locked || !etag}
            submitting={
              activeMutationKind === 'activate' ||
              activeMutationKind === 'cohort' ||
              activeMutationKind === 'release'
                ? activeMutationKind
                : undefined
            }
            onActivate={(configuration_id) => submitLifecycle('activate', { configuration_id })}
            onCohort={(body) => submitLifecycle('cohort', body)}
            onRelease={(body) => submitLifecycle('release', body)}
          />
          {readiness.dispatch_mode !== 'HELD' && (
            <Section padding={6} dividers={['bottom']}>
              <VStack gap={4}>
                <Heading level={2}>Hold dispatch</Heading>
                <Text>
                  This holds dispatch for the application. Releasing dispatch is not available here.
                </Text>
                <VStack
                  as="form"
                  gap={3}
                  maxWidth={720}
                  width="100%"
                  onSubmit={(event) => void holdDispatch(event)}
                >
                  <FormLayout>
                    <TextInput
                      label="Hold reason"
                      value={holdReason}
                      onChange={setHoldReason}
                      isDisabled={locked}
                    />
                    <HStack gap={2} wrap="wrap" justify="start">
                      <Button
                        label="Hold dispatch"
                        type="submit"
                        isLoading={busy && activeMutationKind === 'hold'}
                        isDisabled={locked || !etag}
                      />
                    </HStack>
                  </FormLayout>
                </VStack>
              </VStack>
            </Section>
          )}
          <Section padding={6}>
            <VStack gap={4}>
              <Heading level={2}>Approvals</Heading>
              {approvalError ? (
                <Text role="status">{approvalError}</Text>
              ) : approvals.length ? (
                approvals.map((approval) => (
                  <Text key={approval.id}>
                    {approvalKind(approval.kind)} · {approvalStatus(approval.status)} · expires{' '}
                    {formatDate(approval.expires_at)}
                  </Text>
                ))
              ) : (
                <Text color="secondary">No approvals for this application.</Text>
              )}
            </VStack>
          </Section>
        </>
      )}
    </PageFrame>
  );
}

function dispatchLabel(value: Readiness['dispatch_mode']) {
  return ({ HELD: 'Held', PILOT: 'PILOT', FULL: 'FULL' } as Record<string, string>)[value] ?? value;
}
function operationState(value: Operation['state']) {
  return (
    (
      {
        PENDING: 'Pending',
        RUNNING: 'Running',
        SUCCEEDED: 'Succeeded',
        FAILED: 'Failed',
        SUPERSEDED: 'Superseded',
      } as Record<string, string>
    )[value] ?? value
  );
}
function approvalKind(value: Approval['kind']) {
  return (
    ({ TEST_COHORT: 'Test cohort', PILOT: 'PILOT', FULL: 'FULL' } as Record<string, string>)[
      value
    ] ?? value
  );
}
function approvalStatus(value: Approval['status']) {
  return (
    (
      {
        VALID: 'Valid',
        REVOKED: 'Revoked',
        EXPIRED: 'Expired',
      } as Record<string, string>
    )[value] ?? value
  );
}
