import { useEffect, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertDialog } from '@astryxdesign/core/AlertDialog';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { Dialog, DialogHeader } from '@astryxdesign/core/Dialog';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { MetadataList, MetadataListItem } from '@astryxdesign/core/MetadataList';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Heading } from '@astryxdesign/core/Heading';
import { Link } from '@astryxdesign/core/Link';
import { Section } from '@astryxdesign/core/Section';
import { Table, proportional } from '@astryxdesign/core/Table';
import { Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { ApiError, data, versionedData } from '../../lib/api/client';
import type { components } from '../../lib/api/generated/applications';
import { useWrite } from '../management/data-hooks';
import { QueryState, WriteFeedback } from '../management/shared';
import { useUnsaved } from '../../app/unsaved';
import { Failure, Status } from '../../components/ui/Feedback';
import { PageFrame } from '../../components/ui/PageFrame';
import { SubmitAction } from '../../components/ui/Actions';

type Application = components['schemas']['Application'];
type Grant = components['schemas']['StaffGrant'];
type AppList = components['schemas']['ApplicationListResponse']['data'];
type GrantList = components['schemas']['StaffGrantListResponse']['data'];
type Versioned<T> = { data: T; etag: string };

const pageSize = 20;
const maxInt64 = 9223372036854775807n;

function validStaffID(value: string) {
  if (!/^[1-9][0-9]*$/.test(value)) return false;
  try {
    return BigInt(value) <= maxInt64;
  } catch {
    return false;
  }
}

function byteLength(value: string) {
  return new TextEncoder().encode(value).length;
}

function hasControlCharacters(value: string) {
  return Array.from(value).some(
    (character) => character.codePointAt(0)! < 32 || character.codePointAt(0) === 127,
  );
}

export default function Registry() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const operation = useWrite();
  const setDirty = useUnsaved();
  const [page, setPage] = useState(1);
  const [keyFilter, setKeyFilter] = useState('');
  const [filterInput, setFilterInput] = useState('');
  const [key, setKey] = useState('');
  const [name, setName] = useState('');
  const [formError, setFormError] = useState('');
  const list = useQuery({
    queryKey: ['managed-applications', page, keyFilter],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ page: String(page), limit: String(pageSize) });
      if (keyFilter) query.set('key', keyFilter);
      return data<AppList>(`/admin/apps?${query.toString()}`, signal);
    },
  });

  async function createApp(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    const cleanKey = key;
    const cleanName = name.trim();
    if (!/^[a-z][a-z0-9-]{1,61}[a-z0-9]$/.test(cleanKey)) {
      setFormError(
        'Use a key with 3–63 characters: lowercase letter first, followed by lowercase letters, numbers, or hyphens.',
      );
      return;
    }
    if (!cleanName || byteLength(cleanName) > 128 || hasControlCharacters(cleanName)) {
      setFormError(
        'Name is required, must be at most 128 UTF-8 bytes, and cannot contain control characters.',
      );
      return;
    }
    const result = await operation.write<{ data: Application }>('/admin/apps', 'POST', {
      key: cleanKey,
      name: cleanName,
    });
    if (!result) return;
    setDirty(false);
    await client.invalidateQueries({ queryKey: ['managed-applications'] });
    setKey('');
    setName('');
    navigate(`/apps/admin/applications/${result.data.id}`, { state: { registered: true } });
  }

  return (
    <PageFrame
      title="Application registry"
      description="Manage application identities and explicitly granted staff access."
      footer={
        !list.isPending && !list.isError && ((list.data?.total ?? 0) > 0 || page > 1) ? (
          <HStack gap={2} wrap="wrap" align="center" justify="between">
            <Text>
              Page {page} · {list.data?.total ?? 0} applications
            </Text>
            <HStack gap={2}>
              <Button
                label="Previous"
                size="sm"
                isDisabled={page <= 1 || list.isFetching}
                onClick={() => setPage((current) => current - 1)}
              />
              <Button
                label="Next"
                size="sm"
                isDisabled={page * pageSize >= (list.data?.total ?? 0) || list.isFetching}
                onClick={() => setPage((current) => current + 1)}
              />
            </HStack>
          </HStack>
        ) : undefined
      }
    >
      <Section padding={6}>
        <VStack gap={4}>
          <Heading level={2}>Registered applications</Heading>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              setKeyFilter(filterInput.trim());
              setPage(1);
            }}
          >
            <HStack gap={2} wrap="wrap" align="end">
              <TextInput
                label="Filter by application key"
                value={filterInput}
                onChange={setFilterInput}
              />
              <Button label="Apply filter" type="submit" />
              {keyFilter && (
                <Button
                  label="Clear filter"
                  variant="ghost"
                  onClick={() => {
                    setKeyFilter('');
                    setFilterInput('');
                    setPage(1);
                  }}
                />
              )}
            </HStack>
          </form>
          <QueryState query={list}>
            {list.data?.items.length ? (
              <Table
                data={
                  list.data.items as ((typeof list.data.items)[number] & Record<string, unknown>)[]
                }
                columns={[
                  {
                    key: 'name',
                    header: 'Application',
                    width: proportional(2, { minWidth: 200 }),
                    renderCell: (app) => (
                      <Link href={`/apps/admin/applications/${app.id}`}>{app.name}</Link>
                    ),
                  },
                  {
                    key: 'key',
                    header: 'Key',
                    width: proportional(1, { minWidth: 160 }),
                    renderCell: (app) => <Text>{app.key}</Text>,
                  },
                  {
                    key: 'status',
                    header: 'Status',
                    width: proportional(1, { minWidth: 120 }),
                    renderCell: (app) => <Status value={app.status} />,
                  },
                  {
                    key: 'id',
                    header: 'ID',
                    width: proportional(1, { minWidth: 240 }),
                    renderCell: (app) => <Text>{app.id}</Text>,
                  },
                ]}
                density="balanced"
                hasHover
              />
            ) : (
              <EmptyState
                title={
                  keyFilter ? 'No applications match this filter' : 'No applications registered'
                }
                description={
                  keyFilter
                    ? 'Clear the filter to see every registered application.'
                    : 'Create an application registry below to begin managing it.'
                }
              />
            )}
          </QueryState>
        </VStack>
      </Section>
      <Section padding={6}>
        <VStack gap={4}>
          <Heading level={2}>Register an application</Heading>
          <Text color="secondary">
            New applications begin in Prepared status. Registration does not activate an application
            or grant anyone business access.
          </Text>
          <form onInput={() => setDirty(true)} onSubmit={(event) => void createApp(event)}>
            <VStack gap={3} maxWidth={720} width="100%">
              <FormLayout>
                <TextInput
                  label="Application key"
                  value={key}
                  onChange={setKey}
                  autoComplete="off"
                  isRequired
                  isDisabled={operation.blocked()}
                  description="3–63 ASCII characters; lowercase letter first, no spaces."
                />
                <TextInput
                  label="Application name"
                  value={name}
                  onChange={setName}
                  isRequired
                  isDisabled={operation.blocked()}
                />
                {formError && (
                  <Banner
                    status="error"
                    title="Application was not registered"
                    description={formError}
                    role="alert"
                  />
                )}
                <WriteFeedback operation={operation} />
                <HStack gap={2} wrap="wrap" justify="start">
                  <SubmitAction
                    label={operation.pending() ? 'Registering…' : 'Register application'}
                    isLoading={operation.pending()}
                    isDisabled={operation.blocked()}
                  />
                </HStack>
              </FormLayout>
            </VStack>
          </form>
        </VStack>
      </Section>
    </PageFrame>
  );
}

export function RegistryDetail() {
  const params = useParams();
  const location = useLocation();
  const registered = location.state?.registered === true;
  const client = useQueryClient();
  const operation = useWrite();
  const [writeTarget, setWriteTarget] = useState<'name' | 'grant'>('name');
  const [pendingAction, setPendingAction] = useState<'rename' | 'grant' | 'revoke' | null>(null);
  const [confirmingGrant, setConfirmingGrant] = useState(false);
  const [confirmingReload, setConfirmingReload] = useState(false);
  const [revoking, setRevoking] = useState<Versioned<Grant> | null>(null);
  const [renameError, setRenameError] = useState('');
  const setDirty = useUnsaved();
  const [name, setName] = useState('');
  const [nameDirty, setNameDirty] = useState(false);
  const [saved, setSaved] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [staffID, setStaffID] = useState('');
  const [lookupID, setLookupID] = useState('');
  const [grantNotice, setGrantNotice] = useState('');
  const [grantError, setGrantError] = useState('');
  const [grantConflict, setGrantConflict] = useState(false);
  const [grantPage, setGrantPage] = useState(1);
  const app = useQuery({
    queryKey: ['managed-application', params.managedAppId],
    queryFn: ({ signal }) =>
      versionedData<Application>(`/admin/apps/${params.managedAppId}`, signal),
    enabled: !!params.managedAppId,
  });
  const grants = useQuery({
    queryKey: ['application-staff-grants', params.managedAppId, grantPage],
    queryFn: ({ signal }) =>
      data<GrantList>(
        `/admin/apps/${params.managedAppId}/staff-grants?page=${grantPage}&limit=${pageSize}`,
        signal,
      ),
    enabled: !!params.managedAppId,
  });
  const grantLookup = useQuery({
    queryKey: ['application-staff-grant', params.managedAppId, lookupID],
    queryFn: ({ signal }) =>
      versionedData<Grant>(`/admin/apps/${params.managedAppId}/staff-grants/${lookupID}`, signal),
    enabled: !!lookupID,
    retry: false,
  });

  useEffect(() => {
    const current = app.data?.data;
    if (current && !nameDirty) setName(current.name);
  }, [app.data, nameDirty]);

  async function rename(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (operation.blocked() || conflict || !app.data?.etag) return;
    setSaved(false);
    setConflict(false);
    setRenameError('');
    const cleanName = name.trim();
    if (!cleanName || byteLength(cleanName) > 128 || hasControlCharacters(cleanName)) {
      setRenameError(
        'Name is required, must be at most 128 UTF-8 bytes, and cannot contain control characters.',
      );
      return;
    }
    setWriteTarget('name');
    setPendingAction('rename');
    let result: { data: Application } | undefined;
    try {
      result = await operation.write<{ data: Application }>(
        `/admin/apps/${params.managedAppId}`,
        'PATCH',
        { name: cleanName },
        { ifMatch: app.data.etag },
      );
    } finally {
      setPendingAction(null);
    }
    if (!result) {
      if (operation.status() === 412) setConflict(true);
      return;
    }
    setDirty(false);
    setNameDirty(false);
    setName(result.data.name);
    setSaved(true);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['managed-application', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['managed-applications'] }),
    ]);
    await app.refetch();
  }

  async function refreshApp() {
    const result = await app.refetch();
    if (result.data) {
      setName(result.data.data.name);
      setConflict(false);
      setDirty(false);
      setNameDirty(false);
    }
  }

  async function inspectGrant(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    const id = staffID;
    if (!validStaffID(id)) {
      setGrantError('Enter a positive int64 staff ID without leading zeroes.');
      return;
    }
    setLookupID(id);
    await client.invalidateQueries({
      queryKey: ['application-staff-grant', params.managedAppId, id],
    });
  }

  async function refreshGrant() {
    const result = await grantLookup.refetch();
    if (result.isSuccess || (result.error instanceof ApiError && result.error.status === 404))
      setGrantConflict(false);
  }

  const lookupIsMissing = grantLookup.error instanceof ApiError && grantLookup.error.status === 404;

  async function enableGrant() {
    if (
      operation.blocked() ||
      grantConflict ||
      grantLookup.isFetching ||
      (!grantLookup.isSuccess && !lookupIsMissing)
    )
      return;
    const existing = grantLookup.data;
    const id = lookupID;
    if (!id) return;
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    setWriteTarget('grant');
    setPendingAction('grant');
    const conditions = existing ? { ifMatch: existing.etag } : { ifNoneMatch: '*' as const };
    let result: { data: Grant } | undefined;
    try {
      result = await operation.write<{ data: Grant }>(
        `/admin/apps/${params.managedAppId}/staff-grants/${id}`,
        'PUT',
        { is_active: true },
        conditions,
      );
    } finally {
      setPendingAction(null);
    }
    if (!result) {
      if (operation.status() === 412) setGrantConflict(true);
      setGrantError(operation.error());
      return;
    }
    setGrantNotice(`Access for staff #${id} is active.`);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['application-staff-grants', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['application-staff-grant', params.managedAppId, id] }),
    ]);
    await grantLookup.refetch();
  }

  async function revokeGrant(grant: Versioned<Grant>) {
    if (operation.blocked() || grantConflict || grantLookup.isFetching) return;
    const id = grant.data.staff_id;
    setGrantError('');
    setGrantNotice('');
    setGrantConflict(false);
    setWriteTarget('grant');
    setPendingAction('revoke');
    let result: void | undefined;
    try {
      result = await operation.write<void>(
        `/admin/apps/${params.managedAppId}/staff-grants/${id}`,
        'DELETE',
        undefined,
        { ifMatch: grant.etag },
      );
    } finally {
      setPendingAction(null);
    }
    if (result === undefined && operation.error()) {
      if (operation.status() === 412) setGrantConflict(true);
      setGrantError(operation.error());
      return;
    }
    setGrantNotice(`Access for staff #${id} was revoked.`);
    await Promise.all([
      client.invalidateQueries({ queryKey: ['application-staff-grants', params.managedAppId] }),
      client.invalidateQueries({ queryKey: ['application-staff-grant', params.managedAppId, id] }),
    ]);
    if (lookupID === id) await grantLookup.refetch();
  }

  return (
    <PageFrame
      title={app.data?.data.name ?? 'Application'}
      description="Application identity and staff access grants."
      back={{ label: 'Back to application registry', href: '/apps/admin/applications' }}
      contentWidth={1120}
    >
      {registered && (
        <Banner
          status="success"
          role="status"
          title="Application registry created"
          description="The application is ready for setup."
        />
      )}
      <QueryState query={app}>
        <VStack gap={4}>
          <Section padding={6}>
            <VStack gap={4}>
              <Heading level={2}>Identity</Heading>
              {app.data && (
                <MetadataList>
                  <MetadataListItem label="Application key">{app.data.data.key}</MetadataListItem>
                  <MetadataListItem label="Application ID">{app.data.data.id}</MetadataListItem>
                  <MetadataListItem label="Status">
                    <Status value={app.data.data.status} />
                  </MetadataListItem>
                  <MetadataListItem label="Revision">{app.data.data.revision}</MetadataListItem>
                </MetadataList>
              )}
              <Text color="secondary">
                Prepare credentials and verify the service before device testing.
              </Text>
              <Link
                href={`/apps/admin/applications/${params.managedAppId}/onboarding`}
                isStandalone
              >
                Notification setup
              </Link>
            </VStack>
          </Section>
          <Section padding={6}>
            <VStack gap={4}>
              <Heading level={2}>Rename application</Heading>
              <form
                onInput={() => {
                  setDirty(true);
                  setNameDirty(true);
                  setSaved(false);
                }}
                onSubmit={(event) => void rename(event)}
              >
                <VStack gap={3} maxWidth={720} width="100%">
                  <FormLayout>
                    <TextInput
                      label="Application name"
                      value={name}
                      onChange={setName}
                      isRequired
                      isDisabled={operation.blocked()}
                    />
                    <Text color="secondary">
                      Maximum 128 UTF-8 bytes. Renaming uses the revision most recently read.
                    </Text>
                    {renameError && (
                      <Banner
                        status="error"
                        title="Name not saved"
                        description={renameError}
                        role="alert"
                      />
                    )}
                    {writeTarget === 'name' && <WriteFeedback operation={operation} />}
                    {saved && (
                      <Banner
                        status="success"
                        title="Name updated"
                        description="The application name was updated."
                        role="status"
                      />
                    )}
                    {conflict && (
                      <VStack gap={2}>
                        <Banner
                          status="error"
                          title="Application changed"
                          description="Reload and review the latest name before trying again."
                          role="alert"
                        />
                        <HStack>
                          <Button
                            label="Reload application"
                            onClick={() => {
                              if (nameDirty) setConfirmingReload(true);
                              else void refreshApp();
                            }}
                          />
                        </HStack>
                      </VStack>
                    )}
                    <HStack gap={2} wrap="wrap" justify="start">
                      <SubmitAction
                        label="Save name"
                        isLoading={pendingAction === 'rename'}
                        isDisabled={conflict || !app.data?.etag || operation.blocked()}
                      />
                    </HStack>
                  </FormLayout>
                </VStack>
              </form>
            </VStack>
          </Section>
          <Section padding={6}>
            <VStack gap={4}>
              <Heading level={2}>Staff access</Heading>
              <Text color="secondary">
                Look up staff by ID, then grant or revoke access after reviewing their identity.
              </Text>
              <form onSubmit={(event) => void inspectGrant(event)}>
                <HStack gap={2} wrap="wrap" align="end">
                  <TextInput
                    label="Staff ID"
                    inputMode="numeric"
                    value={staffID}
                    onChange={setStaffID}
                  />
                  <Button label="Inspect access" type="submit" />
                </HStack>
              </form>
              {grantError && (
                <Banner
                  status="error"
                  title="Access change needs review"
                  description={grantError}
                  role="alert"
                />
              )}
              {grantNotice && (
                <Banner
                  status="success"
                  title="Access updated"
                  description={grantNotice}
                  role="status"
                />
              )}
              {lookupID && (
                <Section variant="muted" padding={6}>
                  <VStack gap={2}>
                    <Heading level={3}>Staff #{lookupID}</Heading>
                    <Link href={`/apps/admin/staff/${lookupID}`}>View staff account</Link>
                    {grantLookup.isPending && <Text role="status">Checking access…</Text>}
                    {grantLookup.isError && !lookupIsMissing && (
                      <Failure error={grantLookup.error} retry={() => void refreshGrant()} />
                    )}
                    {grantLookup.isSuccess && (
                      <Text>
                        Status:{' '}
                        <Status value={grantLookup.data.data.is_active ? 'ACTIVE' : 'INACTIVE'} /> ·
                        revision {grantLookup.data.data.revision}
                      </Text>
                    )}
                    {lookupIsMissing && (
                      <Text>
                        No access row exists for this ID. Access can be created only if the staff
                        account remains active.
                      </Text>
                    )}
                    {grantConflict && (
                      <VStack gap={2}>
                        <Banner
                          status="error"
                          title="Access changed"
                          description="Reload to review the current status, then confirm again."
                          role="alert"
                        />
                        <HStack>
                          <Button label="Reload access" onClick={() => void refreshGrant()} />
                        </HStack>
                      </VStack>
                    )}
                    <HStack gap={2} wrap="wrap">
                      <Button
                        label="Grant or restore access"
                        variant="primary"
                        isLoading={pendingAction === 'grant'}
                        isDisabled={
                          (!lookupIsMissing && !grantLookup.isSuccess) ||
                          operation.blocked() ||
                          grantConflict ||
                          grantLookup.isFetching
                        }
                        onClick={() => setConfirmingGrant(true)}
                      />
                      {grantLookup.data?.data.is_active && (
                        <Button
                          label="Revoke access"
                          isLoading={pendingAction === 'revoke'}
                          isDisabled={
                            operation.blocked() || grantConflict || grantLookup.isFetching
                          }
                          onClick={() => setRevoking(grantLookup.data!)}
                        />
                      )}
                    </HStack>
                  </VStack>
                </Section>
              )}
              {writeTarget === 'grant' && <WriteFeedback operation={operation} />}
              <Heading level={3}>All access grants</Heading>
              <QueryState query={grants}>
                {grants.data?.items.length ? (
                  <Table
                    data={
                      grants.data.items as ((typeof grants.data.items)[number] &
                        Record<string, unknown>)[]
                    }
                    columns={[
                      {
                        key: 'staff_id',
                        header: 'Staff ID',
                        width: proportional(1, { minWidth: 120 }),
                      },
                      {
                        key: 'is_active',
                        header: 'Access status',
                        width: proportional(1, { minWidth: 160 }),
                        renderCell: (grant) => (
                          <Status value={grant.is_active ? 'ACTIVE' : 'INACTIVE'} />
                        ),
                      },
                      {
                        key: 'revision',
                        header: 'Revision',
                        width: proportional(1, { minWidth: 120 }),
                      },
                      {
                        key: 'action',
                        header: 'Action',
                        width: proportional(1, { minWidth: 120 }),
                        renderCell: (grant) => (
                          <Button
                            label="Inspect"
                            size="sm"
                            onClick={() => {
                              setStaffID(grant.staff_id);
                              setLookupID(grant.staff_id);
                            }}
                          />
                        ),
                      },
                    ]}
                    density="balanced"
                    hasHover
                  />
                ) : (
                  <EmptyState
                    title="No access grants"
                    description="There are no staff access grants yet."
                  />
                )}
              </QueryState>
              {!grants.isPending &&
                !grants.isError &&
                ((grants.data?.total ?? 0) > 0 || grantPage > 1) && (
                  <HStack gap={2} wrap="wrap" align="center" justify="between">
                    <Text>
                      Page {grantPage} · {grants.data?.total ?? 0} rows
                    </Text>
                    <HStack gap={2}>
                      <Button
                        label="Previous"
                        size="sm"
                        isDisabled={grantPage <= 1 || grants.isFetching}
                        onClick={() => setGrantPage((current) => current - 1)}
                      />
                      <Button
                        label="Next"
                        size="sm"
                        isDisabled={
                          grantPage * pageSize >= (grants.data?.total ?? 0) || grants.isFetching
                        }
                        onClick={() => setGrantPage((current) => current + 1)}
                      />
                    </HStack>
                  </HStack>
                )}
            </VStack>
          </Section>
        </VStack>
      </QueryState>
      <Dialog
        isOpen={confirmingGrant}
        onOpenChange={(open) => {
          if (!open && pendingAction !== 'grant') setConfirmingGrant(false);
        }}
        purpose="required"
      >
        <VStack gap={4}>
          <DialogHeader title={`Grant application access to staff #${lookupID}?`} />
          <Text>
            The staff member gains this application's workspace according to their account role.
          </Text>
          <HStack gap={2} hAlign="end">
            <Button
              label="Back"
              variant="secondary"
              isDisabled={pendingAction === 'grant'}
              onClick={() => setConfirmingGrant(false)}
            />
            <Button
              label="Grant access"
              variant="primary"
              isLoading={pendingAction === 'grant'}
              onClick={() => {
                setConfirmingGrant(false);
                void enableGrant();
              }}
            />
          </HStack>
        </VStack>
      </Dialog>
      <AlertDialog
        isOpen={confirmingReload}
        onOpenChange={(open) => {
          if (!open) setConfirmingReload(false);
        }}
        title="Reload and replace the unsaved application name?"
        description="The unsaved name is discarded and the latest server value is loaded for review."
        actionLabel="Reload application"
        onAction={() => {
          setConfirmingReload(false);
          void refreshApp();
        }}
      />
      <AlertDialog
        isOpen={revoking !== null}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        title={`Revoke application access from staff #${revoking?.data.staff_id}?`}
        description="The staff member loses this application's workspace immediately."
        actionLabel="Revoke access"
        isActionLoading={pendingAction === 'revoke'}
        onAction={() => {
          const grant = revoking;
          setRevoking(null);
          if (grant) void revokeGrant(grant);
        }}
      />
    </PageFrame>
  );
}
