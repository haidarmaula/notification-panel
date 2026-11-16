import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Banner } from '@astryxdesign/core/Banner';
import { Button } from '@astryxdesign/core/Button';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Heading } from '@astryxdesign/core/Heading';
import { Link } from '@astryxdesign/core/Link';
import { Section } from '@astryxdesign/core/Section';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Selector } from '@astryxdesign/core/Selector';
import { Table, proportional } from '@astryxdesign/core/Table';
import { Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { data } from '../../lib/api/client';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
import { useWrite } from '../management/data-hooks';
import { WriteFeedback } from '../management/shared';
import { useUnsaved } from '../../app/unsaved';
import { PageFrame } from '../../components/ui/PageFrame';
import { CreateAction, SubmitAction } from '../../components/ui/Actions';

type StaffId = number | string;
type StaffRole = 'SUPER_ADMIN' | 'ADMIN' | 'OPERATOR' | 'VIEWER';
type StaffMember = {
  id: StaffId;
  role_id: number;
  role_name: StaffRole;
  name: string;
  email: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
};
type StaffPage = {
  data: StaffMember[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
};
type Envelope<T> = { data: T };

const roles: StaffRole[] = ['SUPER_ADMIN', 'ADMIN', 'OPERATOR', 'VIEWER'];
const staffIdPattern = /^[1-9][0-9]{0,18}$/;
const maxStaffId = 9223372036854775807n;

function isStaffId(value: string) {
  return staffIdPattern.test(value) && BigInt(value) <= maxStaffId;
}

function passwordBytes(value: string) {
  return new TextEncoder().encode(value).length;
}

function StaffLink({ id, children }: { id: string; children: React.ReactNode }) {
  return <Link href={`/apps/admin/staff/${id}`}>{children}</Link>;
}

export default function Staff() {
  const [params, setParams] = useSearchParams();
  const [term, setTerm] = useState(() => (params.get('search') ?? '').slice(0, 255));
  const pageNumber = (() => {
    const value = Number(params.get('page') ?? '1');
    return Number.isSafeInteger(value) && value > 0 ? value : 1;
  })();
  const search = params.get('search') ?? '';
  const page = useQuery({
    queryKey: ['admin-staff', search, pageNumber],
    queryFn: ({ signal }) =>
      data<StaffPage>(
        `/staff?page=${pageNumber}&limit=20${search ? `&search=${encodeURIComponent(search)}` : ''}`,
        signal,
      ),
  });
  const profile = useQuery({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ id: StaffId }>('/profile', signal),
  });
  const operation = useWrite();
  const client = useQueryClient();
  const [pendingStaffID, setPendingStaffID] = useState<string | null>(null);
  const profileKnown = profile.isSuccess && profile.data?.id !== undefined;
  const isCurrentStaff = (staff: StaffMember) =>
    profileKnown && String(profile.data!.id) === String(staff.id);

  function applySearch(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next = term.trim().slice(0, 255);
    setParams(next ? { search: next } : {});
  }

  async function toggleActive(staff: StaffMember) {
    if (!profileKnown || isCurrentStaff(staff) || operation.blocked()) return;
    const id = String(staff.id);
    setPendingStaffID(id);
    let result: Envelope<StaffMember> | undefined;
    try {
      result = await operation.write<Envelope<StaffMember>>(`/staff/${id}/status`, 'PATCH', {
        is_active: !staff.is_active,
      });
    } finally {
      setPendingStaffID(null);
    }
    if (result) await client.invalidateQueries({ queryKey: ['admin-staff'] });
  }

  const rows = page.data?.data ?? [];
  const tableRows = rows as (StaffMember & Record<string, unknown>)[];
  return (
    <PageFrame
      title="Staff management"
      description="Manage accounts that can access the dashboard."
      actions={<CreateAction label="Add staff" href="/apps/admin/staff/new" />}
    >
      <Section padding={6}>
        <VStack gap={3}>
          <Text color="secondary">Search by name or email.</Text>
          <form onSubmit={applySearch} role="search">
            <HStack gap={2} wrap="wrap" align="end">
              <TextInput
                label="Search staff"
                type="search"
                value={term}
                onChange={(value) => setTerm(value.slice(0, 255))}
                placeholder="Name or email"
              />
              <Button label="Search" type="submit" />
            </HStack>
          </form>
          <WriteFeedback operation={operation} />
          {page.isPending ? (
            <Loading rows={5} />
          ) : page.isError ? (
            <Failure error={page.error} retry={() => void page.refetch()} />
          ) : rows.length === 0 ? (
            <EmptyState
              title={search ? 'No matching staff accounts' : 'No staff accounts yet'}
              description={
                search
                  ? 'Try a different search term.'
                  : 'Create a staff account to provide dashboard access.'
              }
              actions={
                !search ? (
                  <Link href="/apps/admin/staff/new">Add the first staff account</Link>
                ) : undefined
              }
            />
          ) : (
            <Table
              data={tableRows}
              columns={[
                {
                  key: 'name',
                  header: 'Staff member',
                  width: proportional(2, { minWidth: 220 }),
                  renderCell: (staff) => (
                    <VStack gap={0.5}>
                      <StaffLink id={String(staff.id)}>{staff.name}</StaffLink>
                      <Text type="supporting" color="secondary">
                        {staff.email} · #{staff.id}
                      </Text>
                    </VStack>
                  ),
                },
                { key: 'role_name', header: 'Role', width: proportional(1, { minWidth: 120 }) },
                {
                  key: 'is_active',
                  header: 'Status',
                  width: proportional(1, { minWidth: 120 }),
                  renderCell: (staff) => <Status value={staff.is_active ? 'ACTIVE' : 'INACTIVE'} />,
                },
                {
                  key: 'actions',
                  header: 'Actions',
                  width: proportional(1, { minWidth: 160 }),
                  renderCell: (staff) => (
                    <Button
                      label={`${staff.is_active ? 'Deactivate' : 'Activate'} ${staff.name}${isCurrentStaff(staff) ? ', your account' : !profileKnown ? ', identity not verified' : ''}`}
                      size="sm"
                      isLoading={pendingStaffID === String(staff.id)}
                      isDisabled={operation.blocked() || !profileKnown || isCurrentStaff(staff)}
                      onClick={() => void toggleActive(staff)}
                    />
                  ),
                },
              ]}
              density="balanced"
              hasHover
            />
          )}
          {!page.isPending && !page.isError && (rows.length > 0 || pageNumber > 1) && (
            <HStack gap={2} wrap="wrap" align="center" justify="between">
              <Text>
                {page.data?.total ?? 0} accounts · Page {pageNumber} of{' '}
                {Math.max(page.data?.total_pages ?? 0, 1)}
              </Text>
              <HStack gap={2}>
                <Button
                  label="Previous"
                  size="sm"
                  isDisabled={pageNumber <= 1 || page.isFetching}
                  onClick={() =>
                    setParams({
                      ...(search ? { search } : {}),
                      ...(pageNumber > 2 ? { page: String(pageNumber - 1) } : {}),
                    })
                  }
                />
                <Button
                  label="Next"
                  size="sm"
                  isDisabled={pageNumber >= (page.data?.total_pages ?? 0) || page.isFetching}
                  onClick={() =>
                    setParams({ ...(search ? { search } : {}), page: String(pageNumber + 1) })
                  }
                />
              </HStack>
            </HStack>
          )}
        </VStack>
      </Section>
    </PageFrame>
  );
}

export function StaffEditor() {
  const params = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const passwordOperation = useWrite();
  const setDirty = useUnsaved();
  const editing = Boolean(params.staffId);
  const validId = Boolean(params.staffId && isStaffId(params.staffId));
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<StaffRole>('OPERATOR');
  const [password, setPassword] = useState('');
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [profileError, setProfileError] = useState('');
  const [passwordValidationError, setPasswordValidationError] = useState('');
  const [pendingAction, setPendingAction] = useState<'profile' | 'status' | 'delete' | null>(null);
  const [resetSuccess, setResetSuccess] = useState(false);
  const loadedId = useRef('');
  const createPasswordRef = useRef<HTMLInputElement>(null);
  const currentPasswordRef = useRef<HTMLInputElement>(null);
  const newPasswordRef = useRef<HTMLInputElement>(null);
  const profile = useQuery({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ id: StaffId; role_name: string }>('/profile', signal),
  });
  const profileKnown = profile.isSuccess && profile.data?.id !== undefined;
  const staff = useQuery({
    queryKey: ['admin-staff-detail', params.staffId],
    queryFn: ({ signal }) => data<StaffMember>(`/staff/${params.staffId}`, signal),
    enabled: editing && validId,
  });

  useEffect(() => {
    if (staff.data && loadedId.current !== params.staffId) {
      loadedId.current = params.staffId ?? '';
      setName(staff.data.name);
      setEmail(staff.data.email);
      setRole(staff.data.role_name);
    }
  }, [staff.data, params.staffId]);

  useEffect(
    () => () => {
      if (createPasswordRef.current) createPasswordRef.current.value = '';
      if (currentPasswordRef.current) currentPasswordRef.current.value = '';
      if (newPasswordRef.current) newPasswordRef.current.value = '';
    },
    [],
  );

  const self = Boolean(
    editing &&
    profileKnown &&
    staff.data?.id !== undefined &&
    String(profile.data!.id) === String(staff.data.id),
  );
  const selfSensitiveBlocked = editing && (!profileKnown || self);

  function changed() {
    setDirty(true);
    setProfileError('');
  }

  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setProfileError('');
    if (operation.blocked()) return;
    const body = editing
      ? {
          role: profileKnown && !self && role !== staff.data?.role_name ? role : '',
          name: name.trim(),
          email: email.trim(),
        }
      : { role, name: name.trim(), email: email.trim(), password };
    if (!body.name || !body.email || (!editing && !body.password)) {
      setProfileError('Name, email, and password are required.');
      return;
    }
    if (!editing) {
      const size = passwordBytes(password);
      if (size < 8 || size > 72) {
        setProfileError('The initial password must be 8–72 bytes.');
        return;
      }
    }
    setPendingAction('profile');
    let result: Envelope<StaffMember> | undefined;
    try {
      result = await operation.write<Envelope<StaffMember>>(
        editing ? `/staff/${params.staffId}` : '/staff',
        editing ? 'PATCH' : 'POST',
        body,
      );
    } finally {
      setPendingAction(null);
    }
    if (result) {
      setDirty(false);
      setPassword('');
      if (createPasswordRef.current) createPasswordRef.current.value = '';
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
      if (editing)
        await client.invalidateQueries({ queryKey: ['admin-staff-detail', params.staffId] });
      if (self) await client.invalidateQueries({ queryKey: ['profile'] });
      navigate(`/apps/admin/staff/${result.data.id}`);
    }
  }

  async function resetTargetPassword(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!staff.data || !profileKnown || self || passwordOperation.blocked()) return;
    setResetSuccess(false);
    setPasswordValidationError('');
    if (!currentPassword) {
      setPasswordValidationError('Enter your current password.');
      return;
    }
    const size = passwordBytes(newPassword);
    if (size < 8 || size > 72) {
      setPasswordValidationError('The new password must be 8–72 bytes.');
      return;
    }
    const result = await passwordOperation.write<Envelope<null>>(
      `/staff/${params.staffId}/password`,
      'PATCH',
      { current_password: currentPassword, password: newPassword },
    );
    if (result) {
      setCurrentPassword('');
      setNewPassword('');
      if (currentPasswordRef.current) currentPasswordRef.current.value = '';
      if (newPasswordRef.current) newPasswordRef.current.value = '';
      setResetSuccess(true);
    }
  }

  async function remove() {
    if (!staff.data || !profileKnown || self || operation.blocked()) return;
    if (
      !window.confirm(
        `Delete staff account ${staff.data.name} (${staff.data.email})? This cannot be undone.`,
      )
    )
      return;
    setPendingAction('delete');
    let result: Envelope<null> | undefined;
    try {
      result = await operation.write<Envelope<null>>(`/staff/${params.staffId}`, 'DELETE');
    } finally {
      setPendingAction(null);
    }
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
      navigate('/apps/admin/staff');
    }
  }

  async function toggleStatus() {
    if (!staff.data || !profileKnown || self || operation.blocked()) return;
    setPendingAction('status');
    let result: Envelope<StaffMember> | undefined;
    try {
      result = await operation.write<Envelope<StaffMember>>(
        `/staff/${params.staffId}/status`,
        'PATCH',
        { is_active: !staff.data.is_active },
      );
    } finally {
      setPendingAction(null);
    }
    if (result) {
      await client.invalidateQueries({ queryKey: ['admin-staff-detail', params.staffId] });
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
    }
  }

  return (
    <PageFrame
      title={editing ? 'Edit staff account' : 'Add staff account'}
      description={
        editing
          ? 'Update staff account details and access.'
          : 'Create an account for a member of the administration team.'
      }
      back={{ label: 'Back to staff', href: '/apps/admin/staff' }}
      contentWidth={720}
    >
      {!editing || validId ? (
        staff.isPending && editing ? (
          <Loading />
        ) : staff.isError && editing ? (
          <Failure error={staff.error} retry={() => void staff.refetch()} />
        ) : (
          <VStack gap={3}>
            <Section padding={6}>
              <VStack gap={4}>
                <Heading level={2}>Account information</Heading>
                <form onInput={changed} onSubmit={(event) => void save(event)}>
                  <FormLayout>
                    <TextInput
                      label="Name"
                      value={name}
                      onChange={setName}
                      isRequired
                      isDisabled={operation.blocked()}
                    />
                    <TextInput
                      label="Sign-in email"
                      type="email"
                      value={email}
                      onChange={setEmail}
                      isRequired
                      isDisabled={operation.blocked()}
                    />
                    <Selector
                      label="Role"
                      options={roles}
                      value={role}
                      onChange={(value) => setRole(value as StaffRole)}
                      isDisabled={operation.blocked() || selfSensitiveBlocked}
                    />
                    {self && (
                      <Text color="secondary">Your account role cannot be changed here.</Text>
                    )}
                    {editing && !profileKnown && (
                      <Text role="status">
                        Your identity could not be verified. Role changes and sensitive actions are
                        disabled.
                      </Text>
                    )}
                    {!editing && (
                      <>
                        <TextInput
                          ref={createPasswordRef}
                          label="Initial password"
                          type="password"
                          autoComplete="new-password"
                          value={password}
                          onChange={setPassword}
                          isRequired
                          isDisabled={operation.blocked()}
                          description="Use 8–72 bytes. The password will not be shown again."
                        />
                      </>
                    )}
                    {profileError && (
                      <Banner
                        status="error"
                        title="Account was not saved"
                        description={profileError}
                        role="alert"
                      />
                    )}
                    <WriteFeedback operation={operation} />
                    <HStack gap={2} wrap="wrap" justify="start">
                      <SubmitAction
                        label={
                          pendingAction === 'profile'
                            ? 'Saving…'
                            : editing
                              ? 'Save changes'
                              : 'Create staff account'
                        }
                        isLoading={pendingAction === 'profile'}
                        isDisabled={operation.blocked()}
                      />
                    </HStack>
                  </FormLayout>
                </form>
              </VStack>
            </Section>
            {editing && staff.data && (
              <>
                <Section padding={6}>
                  <VStack gap={4}>
                    <Heading level={2}>Account access</Heading>
                    <Text color="secondary">Sign-in status for {staff.data.email}.</Text>
                    <HStack>
                      <Status value={staff.data.is_active ? 'ACTIVE' : 'INACTIVE'} />
                    </HStack>
                    <HStack>
                      <Button
                        label={staff.data.is_active ? 'Deactivate account' : 'Activate account'}
                        isLoading={pendingAction === 'status'}
                        isDisabled={operation.blocked() || selfSensitiveBlocked}
                        onClick={() => void toggleStatus()}
                      />
                    </HStack>
                    {self && (
                      <Text color="secondary">
                        The account currently in use cannot be deactivated here.
                      </Text>
                    )}
                  </VStack>
                </Section>
                <Section padding={6}>
                  <VStack gap={4}>
                    <Heading level={2}>Set staff password</Heading>
                    <Text color="secondary">
                      Confirm with your current password before setting a new password.
                    </Text>
                    {profileKnown && !self ? (
                      <form onSubmit={(event) => void resetTargetPassword(event)}>
                        <VStack gap={3} maxWidth={720} width="100%">
                          <FormLayout>
                            <TextInput
                              ref={currentPasswordRef}
                              label="Your current password"
                              type="password"
                              autoComplete="current-password"
                              value={currentPassword}
                              onChange={(value) => {
                                setCurrentPassword(value);
                                setResetSuccess(false);
                              }}
                              isRequired
                              isDisabled={passwordOperation.blocked()}
                            />
                            <TextInput
                              ref={newPasswordRef}
                              label="Staff member’s new password"
                              type="password"
                              autoComplete="new-password"
                              value={newPassword}
                              onChange={(value) => {
                                setNewPassword(value);
                                setResetSuccess(false);
                              }}
                              isRequired
                              isDisabled={passwordOperation.blocked()}
                              description="Use 8–72 bytes. The change takes effect the next time the staff member signs in."
                            />
                            {passwordValidationError && (
                              <Banner
                                status="error"
                                title="Password was not changed"
                                description={passwordValidationError}
                                role="alert"
                              />
                            )}
                            <WriteFeedback operation={passwordOperation} />
                            {resetSuccess && (
                              <Banner
                                status="success"
                                title="Password updated"
                                description="The staff password was updated."
                                role="status"
                              />
                            )}
                            <HStack gap={2} wrap="wrap" justify="start">
                              <SubmitAction
                                label={passwordOperation.pending() ? 'Saving…' : 'Set password'}
                                isLoading={passwordOperation.pending()}
                                isDisabled={passwordOperation.blocked()}
                              />
                            </HStack>
                          </FormLayout>
                        </VStack>
                      </form>
                    ) : self ? (
                      <Text>
                        To change your own password, open{' '}
                        <Link href="/apps/settings">account settings</Link>. You will be asked to
                        sign in again afterward.
                      </Text>
                    ) : null}
                  </VStack>
                </Section>
                <Section padding={6}>
                  <VStack gap={4}>
                    <Heading level={2}>Delete account</Heading>
                    <Text color="secondary">Deleted staff accounts cannot be restored.</Text>
                    <WriteFeedback operation={operation} />
                    <HStack>
                      <Button
                        label="Delete staff account"
                        variant="destructive"
                        isLoading={pendingAction === 'delete'}
                        isDisabled={operation.blocked() || selfSensitiveBlocked}
                        onClick={() => void remove()}
                      />
                    </HStack>
                    {self && (
                      <Text color="secondary">The account currently in use cannot be deleted.</Text>
                    )}
                  </VStack>
                </Section>
              </>
            )}
          </VStack>
        )
      ) : (
        <Banner
          status="error"
          title="Invalid staff ID"
          description="Return to the staff directory and choose a valid account."
          role="alert"
        />
      )}
    </PageFrame>
  );
}
