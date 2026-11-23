import type { Page, Route } from '@playwright/test';
import type { components } from '../src/lib/api/generated/onboarding';

type Operation = components['schemas']['Operation'];
type OperationKind = Operation['kind'];

export const lifecycleIDs = {
  app: '11111111-1111-4111-8111-111111111111',
  profile: '22222222-2222-4222-8222-222222222222',
  configuration: '33333333-3333-4333-8333-333333333333',
  approval: '44444444-4444-4444-8444-444444444444',
  pilotApproval: '44444444-4444-4444-8444-555555555555',
  fullApproval: '44444444-4444-4444-8444-666666666666',
  cohort: '55555555-5555-4555-8555-555555555555',
  activation: '66666666-6666-4666-8666-666666666666',
  dryRun: '77777777-7777-4777-8777-777777777777',
  pilot: '88888888-8888-4888-8888-888888888888',
  full: '99999999-9999-4999-8999-999999999999',
  androidOne: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  androidTwo: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
  iosOne: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
};

type Terminal = 'SUCCEEDED' | 'FAILED';
type LifecycleOptions = {
  initialStatus?: string;
  activation?: Terminal;
  dryRun?: Terminal;
  pilot?: Terminal;
  full?: Terminal;
  staleOnce?: string;
  replayOnce?: string;
  allPassedTargets?: boolean;
  pendingPolls?: number;
};

const api = `/bff/api/admin/apps/${lifecycleIDs.app}/onboarding`;

/** Installs a deterministic, network-isolated backend fixture for the full lifecycle screen. */
export async function installOnboardingLifecycleFixture(
  page: Page,
  options: LifecycleOptions = {},
) {
  let revision = 8;
  let status = options.initialStatus ?? 'ACTIVE';
  let dispatch = 'HELD';
  let cohortID: string | null = lifecycleIDs.cohort;
  let activationState: Operation['state'] | null = null;
  let dryRunState: Operation['state'] | null = null;
  let pilotState: Operation['state'] | null = null;
  let fullState: Operation['state'] | null = null;
  let targetsRecorded = false;
  let lastOperation: Operation | null = null;
  let targetInstallationIDs: string[] = [];
  const accepted = new Map<
    string,
    { key: string; etag: string; body: unknown; response: unknown }
  >();
  const pollCounts = new Map<string, number>();
  const requests: Array<{
    path: string;
    method: string;
    body: unknown;
    key?: string;
    etag?: string;
    csrf?: string;
  }> = [];
  const installationPage = (after: string | null) =>
    after
      ? {
          installations: [
            {
              installation_id: lifecycleIDs.iosOne,
              platform: 'IOS',
              last_seen_at: '2026-11-20T10:00:00Z',
            },
          ],
          next_after: null,
        }
      : {
          installations: [
            {
              installation_id: lifecycleIDs.androidOne,
              platform: 'ANDROID',
              last_seen_at: '2026-11-21T10:00:00Z',
            },
            {
              installation_id: lifecycleIDs.androidTwo,
              platform: 'ANDROID',
              last_seen_at: '2026-11-20T10:00:00Z',
            },
          ],
          next_after: lifecycleIDs.androidTwo,
        };
  const etag = () => `"onboarding:${lifecycleIDs.app}:${revision}"`;
  const readiness = () => ({
    application_id: lifecycleIDs.app,
    application_status: status,
    dispatch_mode: dispatch,
    revision: String(revision),
    environment: 'staging',
    current_configuration_id: status === 'ACTIVE' ? lifecycleIDs.configuration : null,
    candidate_configuration_id: status === 'PROVISIONING' ? lifecycleIDs.configuration : null,
    enrollment_profile_id: lifecycleIDs.profile,
    cohort_id: cohortID,
    checks_configuration_id: lifecycleIDs.configuration,
    checks: [
      {
        code: 'LOCAL_FORMAT',
        outcome: 'PASSED',
        checked_at: '2026-11-20T10:00:00Z',
        expires_at: null,
      },
      {
        code: 'OAUTH',
        outcome: 'PASSED',
        checked_at: '2026-11-20T10:00:00Z',
        expires_at: '2099-01-01T00:00:00Z',
      },
      {
        code: 'TARGET_PERMISSION',
        outcome: targetsRecorded ? 'PASSED' : 'NOT_CHECKED',
        checked_at: targetsRecorded ? '2026-11-20T10:00:00Z' : null,
        expires_at: null,
      },
      {
        code: 'DEVICE_DELIVERY',
        outcome: dispatch === 'FULL' ? 'PASSED' : 'NOT_CHECKED',
        checked_at: dispatch === 'FULL' ? '2026-11-20T10:00:00Z' : null,
        expires_at: null,
      },
    ],
    blockers: [],
    next_actions:
      status === 'ACTIVE' && targetsRecorded
        ? dispatch === 'HELD'
          ? ['RELEASE_PILOT']
          : dispatch === 'PILOT'
            ? ['RELEASE_FULL']
            : []
        : [],
    last_operation: lastOperation,
  });
  const operationState = (kind: string): Operation['state'] => {
    if (kind === 'ACTIVATE') return activationState ?? 'PENDING';
    if (kind === 'TEST_COHORT') return dryRunState ?? 'PENDING';
    if (kind === 'RELEASE_PILOT') return pilotState ?? 'PENDING';
    if (kind === 'RELEASE_FULL') return fullState ?? 'PENDING';
    return 'PENDING';
  };
  const operationIDFor = (kind: string) => {
    if (kind === 'ACTIVATE') return lifecycleIDs.activation;
    if (kind === 'TEST_COHORT') return lifecycleIDs.dryRun;
    if (kind === 'RELEASE_PILOT') return lifecycleIDs.pilot;
    return lifecycleIDs.full;
  };
  const terminalFor = (kind: string): Terminal => {
    if (kind === 'ACTIVATE') return options.activation ?? 'SUCCEEDED';
    if (kind === 'TEST_COHORT') return options.dryRun ?? 'SUCCEEDED';
    if (kind === 'RELEASE_PILOT') return options.pilot ?? 'SUCCEEDED';
    return options.full ?? 'SUCCEEDED';
  };
  const applyTerminal = (kind: string, state: Operation['state']) => {
    if (kind === 'ACTIVATE') {
      activationState = state;
      if (state === 'SUCCEEDED') status = 'ACTIVE';
    } else if (kind === 'TEST_COHORT') {
      dryRunState = state;
      if (state === 'SUCCEEDED') targetsRecorded = true;
    } else if (kind === 'RELEASE_PILOT') {
      pilotState = state;
      if (state === 'SUCCEEDED') dispatch = 'PILOT';
    } else if (kind === 'RELEASE_FULL') {
      fullState = state;
      if (state === 'SUCCEEDED') dispatch = 'FULL';
    }
  };
  const respond = (
    route: Route,
    statusCode: number,
    data: unknown,
    headers: Record<string, string> = {},
  ) =>
    route.fulfill({
      status: statusCode,
      contentType: 'application/json',
      headers: { 'Cache-Control': 'no-store', ...headers },
      body: JSON.stringify({
        success: statusCode < 400,
        message: statusCode < 400 ? 'ok' : 'conflict',
        data,
      }),
    });

  await page.route(`**/bff/api/admin/apps/${lifecycleIDs.app}`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        data: { id: lifecycleIDs.app, name: 'Lifecycle fixture', key: 'lifecycle-fixture' },
      }),
    }),
  );
  await page.route(`**${api}**`, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const headers = request.headers();
    const body = request.postDataJSON?.() ?? null;
    requests.push({
      path: path + url.search,
      method: request.method(),
      body,
      key: headers['idempotency-key'],
      etag: headers['if-match'],
      csrf: headers['x-csrf-token'],
    });
    if (path === api && request.method() === 'GET')
      return respond(route, 200, readiness(), { ETag: etag() });
    if (path.endsWith('/enrollment-profiles'))
      return respond(route, 200, {
        items: [
          { id: lifecycleIDs.profile, name: 'Fixture enrollment', platforms: ['ANDROID', 'IOS'] },
        ],
      });
    if (path.endsWith('/approvals'))
      return respond(route, 200, {
        items: [
          {
            id: lifecycleIDs.approval,
            kind: 'TEST_COHORT',
            status: 'VALID',
            expires_at: '2099-01-01T00:00:00Z',
          },
          {
            id: lifecycleIDs.pilotApproval,
            kind: 'PILOT',
            status: 'VALID',
            expires_at: '2099-01-01T00:00:00Z',
          },
          {
            id: lifecycleIDs.fullApproval,
            kind: 'FULL',
            status: 'VALID',
            expires_at: '2099-01-01T00:00:00Z',
          },
        ],
      });
    if (path.endsWith('/test-installations'))
      return respond(route, 200, installationPage(url.searchParams.get('after')));
    if (path.endsWith('/targets'))
      return respond(
        route,
        200,
        targetsRecorded
          ? targetInstallationIDs.map((installation_id, index) => {
              const platform = installation_id === lifecycleIDs.iosOne ? 'IOS' : 'ANDROID';
              const outcome = options.allPassedTargets ? 'PASSED' : index === 0 ? 'FAILED' : null;
              return {
                installation_id,
                platform,
                outcome,
                failure_code: outcome === 'FAILED' ? 'TOKEN_REJECTED' : null,
                checked_at: outcome === null ? null : '2026-11-21T10:00:00Z',
              };
            })
          : [],
      );
    const operationMatch = path.match(/\/operations\/([0-9a-f-]+)$/);
    if (operationMatch) {
      const kind =
        Object.entries({
          [lifecycleIDs.activation]: 'ACTIVATE',
          [lifecycleIDs.dryRun]: 'TEST_COHORT',
          [lifecycleIDs.pilot]: 'RELEASE_PILOT',
          [lifecycleIDs.full]: 'RELEASE_FULL',
        }).find(([id]) => id === operationMatch[1])?.[1] ?? 'ACTIVATE';
      const current = operationState(kind);
      const polls = (pollCounts.get(kind) ?? 0) + 1;
      pollCounts.set(kind, polls);
      const remainPending =
        (current === 'PENDING' || current === 'RUNNING') && polls <= (options.pendingPolls ?? 0);
      const next = remainPending
        ? 'PENDING'
        : current === 'PENDING' || current === 'RUNNING'
          ? terminalFor(kind)
          : current;
      applyTerminal(kind, next);
      const polled: Operation = {
        id: operationMatch[1],
        kind: kind as OperationKind,
        state: next,
        created_at: '2026-11-21T10:00:00Z',
        completed_at: next === 'PENDING' ? null : '2026-11-21T10:00:01Z',
        failure_code: next === 'FAILED' ? 'FIXTURE_FAILURE' : null,
        resulting_revision: next === 'SUCCEEDED' ? String(revision) : null,
      };
      if (lastOperation?.id === polled.id) lastOperation = polled;
      return respond(route, 200, polled);
    }
    if (request.method() === 'GET') throw new Error(`Unexpected lifecycle GET ${path}`);
    const replay = accepted.get(headers['idempotency-key'] ?? '');
    if (replay) {
      if (JSON.stringify(body) !== JSON.stringify(replay.body))
        return respond(route, 409, { code: 'IDEMPOTENCY_CONFLICT' });
      return respond(route, 202, replay.response, {
        ETag: etag(),
        Location: `${api}/operations/${(replay.response as { id: string }).id}`,
        'Idempotency-Replayed': 'true',
      });
    }
    if (options.staleOnce && path.endsWith(options.staleOnce)) {
      options.staleOnce = undefined;
      revision++;
      return respond(route, 412, null);
    }
    const action = path.endsWith('/activate')
      ? 'ACTIVATE'
      : path.endsWith('/test-cohort')
        ? 'TEST_COHORT'
        : body?.mode === 'PILOT'
          ? 'RELEASE_PILOT'
          : body?.mode === 'FULL'
            ? 'RELEASE_FULL'
            : '';
    if (!action) throw new Error(`Unexpected lifecycle mutation ${request.method()} ${path}`);
    if (!headers['idempotency-key'] || headers['if-match'] !== etag())
      return respond(route, 412, null);
    const id = operationIDFor(action);
    const operation: Operation = {
      id,
      kind: action,
      state: 'PENDING',
      created_at: '2026-11-21T10:00:00Z',
      completed_at: null,
      failure_code: null,
      resulting_revision: null,
    };
    lastOperation = operation;
    accepted.set(headers['idempotency-key'], {
      key: headers['idempotency-key'],
      etag: headers['if-match'],
      body,
      response: operation,
    });
    if (action === 'ACTIVATE') activationState = 'PENDING';
    else if (action === 'TEST_COHORT') {
      dryRunState = 'PENDING';
      targetInstallationIDs = (body as { installation_ids: string[] }).installation_ids;
    } else if (action === 'RELEASE_PILOT') pilotState = 'PENDING';
    else fullState = 'PENDING';
    revision++;
    if (options.replayOnce && path.endsWith(options.replayOnce)) {
      options.replayOnce = undefined;
      return route.abort('failed');
    }
    return respond(route, 202, operation, {
      ETag: etag(),
      Location: `${api}/operations/${id}`,
      'Idempotency-Replayed': 'false',
    });
  });
  return { requests, api, etag: () => etag() };
}
