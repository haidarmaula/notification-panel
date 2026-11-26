import { useState } from 'react';
import { Navigate, Outlet, useLocation, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { AppShell } from '@astryxdesign/core/AppShell';
import { TopNav, TopNavHeading } from '@astryxdesign/core/TopNav';
import { SideNav, SideNavHeading, SideNavItem, SideNavSection } from '@astryxdesign/core/SideNav';
import { VStack, HStack } from '@astryxdesign/core/Stack';
import { Text } from '@astryxdesign/core/Text';
import { Button } from '@astryxdesign/core/Button';
import { IconButton } from '@astryxdesign/core/IconButton';
import { Icon } from '@astryxdesign/core/Icon';
import { Avatar } from '@astryxdesign/core/Avatar';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Banner } from '@astryxdesign/core/Banner';
import { Divider } from '@astryxdesign/core/Divider';
import { DropdownMenu } from '@astryxdesign/core/DropdownMenu';
import { SegmentedControl, SegmentedControlItem } from '@astryxdesign/core/SegmentedControl';
import { Table, proportional, type TableColumn } from '@astryxdesign/core/Table';
import {
  BellIcon,
  Squares2X2Icon,
  PaperAirplaneIcon,
  ChartBarIcon,
  UsersIcon,
  UserGroupIcon,
  DocumentDuplicateIcon,
  QueueListIcon,
  Cog6ToothIcon,
  ArrowRightOnRectangleIcon,
  MoonIcon,
  SunIcon,
  ViewColumnsIcon,
  ChevronUpDownIcon,
  DevicePhoneMobileIcon,
} from '@heroicons/react/24/outline';
import { isThemeSlug, themeLabels, useAppearance } from '../app/appearance-context';
import { useAuth } from '../app/auth-context';
import { isLocalTestDevicesPath, localTestDevicesPath } from '../app/local-test-devices';
import { data, errorMessage } from '../lib/api/client';
import type { Application } from '../lib/api/types';
import { Failure, Loading, Status } from '../components/ui/Feedback';
import { PageFrame } from '../components/ui/PageFrame';
import { Link } from '@astryxdesign/core/Link';

export function Workspace() {
  const auth = useAuth();
  const params = useParams();
  const location = useLocation();
  const appearance = useAppearance();
  const isMobile = useMediaQuery('(width < 768px)');
  const systemPrefersDark = useMediaQuery('(prefers-color-scheme: dark)');
  const sidebarCollapsed = appearance.sidebarCollapsed && !isMobile;
  const selectedAppearance =
    appearance.appearance === 'system'
      ? systemPrefersDark
        ? 'dark'
        : 'light'
      : appearance.appearance;
  const [mobileOpen, setMobileOpen] = useState(false);
  const [logoutError, setLogoutError] = useState('');
  const [leaving, setLeaving] = useState(false);
  const profile = useQuery({
    queryKey: ['profile'],
    queryFn: ({ signal }) =>
      data<{ name: string; email: string; role_name: string; is_active: boolean }>(
        '/profile',
        signal,
      ),
    enabled: auth.authenticated(),
  });
  const application = useQuery({
    queryKey: ['application', params.appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${params.appId}`, signal),
    enabled: auth.authenticated() && !!params.appId,
  });
  async function logout() {
    if (leaving) return;
    setLeaving(true);
    setLogoutError('');
    try {
      if (!(await auth.logout())) setLeaving(false);
    } catch (error) {
      setLogoutError(errorMessage(error));
      setLeaving(false);
    }
  }
  if (auth.session.isPending) return <Loading />;
  if (auth.session.isError)
    return <Failure error={auth.session.error} retry={() => void auth.session.refetch()} />;
  if (!auth.authenticated()) {
    const state = isLocalTestDevicesPath(location.pathname)
      ? { returnTo: location.pathname }
      : undefined;
    return <Navigate to="/login" state={state} replace />;
  }
  const appBase = `/apps/${params.appId}`;
  const showRealFCMTools = isLocalTestDevicesPath(`${appBase}/test-devices`);
  const routes = [
    { label: 'Overview', path: `${appBase}/overview`, icon: ChartBarIcon },
    { label: 'Campaigns', path: `${appBase}/campaigns`, icon: PaperAirplaneIcon },
    { label: 'Templates', path: `${appBase}/templates`, icon: DocumentDuplicateIcon },
    { label: 'Delivery logs', path: `${appBase}/logs`, icon: QueueListIcon },
    { label: 'Audience & devices', path: `${appBase}/audience`, icon: UsersIcon },
    { label: 'Segments', path: `${appBase}/segments`, icon: UserGroupIcon },
    { label: 'Upload history', path: `${appBase}/uploads`, icon: DocumentDuplicateIcon },
    { label: 'Application settings', path: `${appBase}/settings`, icon: Cog6ToothIcon },
    ...(showRealFCMTools
      ? [{ label: 'Test Devices', path: localTestDevicesPath, icon: DevicePhoneMobileIcon }]
      : []),
  ];
  const nav = (
    <SideNav
      collapsible={{
        isCollapsed: sidebarCollapsed,
        onCollapsedChange: appearance.setSidebarCollapsed,
        hasButton: false,
      }}
      header={
        <SideNavHeading
          heading={application.data?.name ?? 'Your applications'}
          subheading={application.data?.key ?? 'Notification workspace'}
        />
      }
      footer={
        <VStack gap={2} padding={2}>
          {!isMobile && (
            <SideNavItem
              label={sidebarCollapsed ? 'Expand Sidebar' : 'Collapse Sidebar'}
              icon={ViewColumnsIcon}
              onClick={() => appearance.setSidebarCollapsed(!appearance.sidebarCollapsed)}
            />
          )}
          {!isMobile && <Divider />}
          <DropdownMenu
            placement="above"
            presentation="adaptive"
            renderTrigger={(triggerProps) => (
              <Button
                {...triggerProps}
                aria-controls={
                  triggerProps['aria-expanded'] ? triggerProps['aria-controls'] : undefined
                }
                label="Account menu"
                tooltip={sidebarCollapsed ? (profile.data?.name ?? 'My account') : undefined}
                variant="ghost"
                width="100%"
                isIconOnly={sidebarCollapsed}
                isDisabled={leaving}
                icon={<Avatar name={profile.data?.name ?? 'Staff'} size="sm" />}
                endContent={
                  sidebarCollapsed ? undefined : <Icon icon={ChevronUpDownIcon} size="sm" />
                }
              >
                {!sidebarCollapsed && (
                  <VStack gap={0}>
                    <Text weight="semibold" maxLines={1}>
                      {profile.data?.name ?? 'Staff account'}
                    </Text>
                    <Text type="supporting" color="secondary" maxLines={1}>
                      {profile.data?.email ?? 'Account settings'}
                    </Text>
                  </VStack>
                )}
              </Button>
            )}
            items={[
              {
                label: 'My account',
                href: '/apps/settings',
                icon: Cog6ToothIcon,
                onClick: () => setMobileOpen(false),
              },
              {
                label: leaving ? 'Signing out…' : 'Sign out',
                icon: ArrowRightOnRectangleIcon,
                onClick: () => void logout(),
                isDisabled: leaving,
              },
            ]}
          />
          {logoutError && (
            <Banner status="error" title="Sign-out failed" description={logoutError} />
          )}
        </VStack>
      }
    >
      <SideNavItem
        label="Applications"
        icon={Squares2X2Icon}
        isSelected={location.pathname === '/apps'}
        href={'/apps'}
        onClick={() => setMobileOpen(false)}
      />
      {params.appId && (
        <SideNavSection title="Workspace">
          {params.appId &&
            routes.map((item) => (
              <SideNavItem
                key={item.path}
                label={item.label}
                icon={item.icon}
                isSelected={location.pathname.startsWith(item.path)}
                href={item.path}
                onClick={() => setMobileOpen(false)}
              />
            ))}
          {params.appId &&
            profile.data?.is_active &&
            ['SUPER_ADMIN', 'ADMIN'].includes(profile.data.role_name) && (
              <SideNavItem
                label="Activity audit"
                icon={QueueListIcon}
                isSelected={location.pathname === `${appBase}/audit`}
                href={`${appBase}/audit`}
                onClick={() => setMobileOpen(false)}
              />
            )}
        </SideNavSection>
      )}
      {profile.data?.is_active && profile.data.role_name === 'SUPER_ADMIN' && (
        <SideNavSection title="Administration">
          <SideNavItem
            label="Manage staff"
            icon={UsersIcon}
            isSelected={location.pathname.startsWith('/apps/admin/staff')}
            href={'/apps/admin/staff'}
            onClick={() => setMobileOpen(false)}
          />
          <SideNavItem
            label="Application registry"
            icon={Squares2X2Icon}
            isSelected={location.pathname.startsWith('/apps/admin/applications')}
            href={'/apps/admin/applications'}
            onClick={() => setMobileOpen(false)}
          />
        </SideNavSection>
      )}
      <SideNavSection title="Account">
        <SideNavItem
          label="My account"
          icon={Cog6ToothIcon}
          isSelected={location.pathname === '/apps/settings'}
          href={'/apps/settings'}
          onClick={() => setMobileOpen(false)}
        />
        <SideNavItem label="API reference" icon={DocumentDuplicateIcon} href="/docs/" />
      </SideNavSection>
    </SideNav>
  );
  return (
    <AppShell
      contentPadding={0}
      mobileNav={{ isOpen: mobileOpen, onOpenChange: setMobileOpen }}
      topNav={
        <TopNav
          heading={
            <TopNavHeading
              heading="hello."
              headingHref="/apps"
              logo={<Icon icon={BellIcon} size="lg" />}
            />
          }
          startContent={
            !isMobile && (
              <Text type="supporting" color="secondary">
                {['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
                  ? 'Local workspace'
                  : window.location.host}
              </Text>
            )
          }
          endContent={
            <HStack gap={2}>
              {!isMobile && (
                <SegmentedControl
                  value={appearance.theme}
                  label="Design theme"
                  size="sm"
                  onChange={(value) => {
                    if (isThemeSlug(value)) appearance.setTheme(value);
                  }}
                >
                  {Object.entries(themeLabels).map(([value, label]) => (
                    <SegmentedControlItem key={value} value={value} label={label} />
                  ))}
                </SegmentedControl>
              )}
              <IconButton
                label={selectedAppearance === 'dark' ? 'Use light mode' : 'Use dark mode'}
                variant="ghost"
                icon={<Icon icon={selectedAppearance === 'dark' ? SunIcon : MoonIcon} size="sm" />}
                onClick={() =>
                  appearance.setAppearance(selectedAppearance === 'dark' ? 'light' : 'dark')
                }
              />
            </HStack>
          }
        />
      }
      sideNav={nav}
    >
      {params.appId && application.isPending ? (
        <PageFrame title="Loading application">
          <Loading />
        </PageFrame>
      ) : application.isError ? (
        <PageFrame title="Application unavailable">
          <Failure error={application.error} retry={() => void application.refetch()} />
        </PageFrame>
      ) : params.appId && application.data?.status !== 'ACTIVE' ? (
        <PageFrame title="Application unavailable">
          <EmptyState
            title="Application unavailable"
            description="Campaigns are available when the application is active."
            actions={
              <Button
                label="Back to applications"
                href="/apps"
                onClick={() => setMobileOpen(false)}
              />
            }
          />
        </PageFrame>
      ) : (
        <Outlet key={location.pathname} />
      )}
    </AppShell>
  );
}
export default function Applications() {
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ['applications', page],
    queryFn: ({ signal }) =>
      data<{ items: Application[]; total: number }>(`/apps?page=${page}&limit=20`, signal),
  });
  const columns: TableColumn<Application>[] = [
    {
      key: 'name',
      header: 'Application',
      width: proportional(2, { minWidth: 200 }),
      renderCell: (app) => (
        <Link href={`/apps/${app.id}/campaigns`} weight="semibold">
          {app.name}
        </Link>
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
  ];
  return (
    <PageFrame
      title="Choose application"
      description="Each application has its own campaigns and recipients."
      footer={
        !query.isPending && !query.isError && (!!query.data?.items.length || page > 1) ? (
          <HStack gap={3} wrap="wrap" hAlign="between" vAlign="center">
            <Text>Page {page}</Text>
            <HStack gap={2}>
              <Button
                label="Previous"
                size="sm"
                isDisabled={page === 1 || query.isFetching}
                onClick={() => setPage((value) => value - 1)}
              />
              <Button
                label="Next"
                size="sm"
                isDisabled={!query.data || page * 20 >= query.data.total || query.isFetching}
                onClick={() => setPage((value) => value + 1)}
              />
            </HStack>
          </HStack>
        ) : undefined
      }
    >
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <Failure error={query.error} retry={() => void query.refetch()} />
      ) : !query.data?.items.length ? (
        <EmptyState
          title="No applications yet"
          description="Ask your administrator to grant application access."
        />
      ) : (
        <Table density="balanced" hasHover data={query.data.items} columns={columns} idKey="id" />
      )}
    </PageFrame>
  );
}
