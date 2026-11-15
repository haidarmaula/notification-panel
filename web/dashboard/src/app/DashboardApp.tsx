import { Component, Suspense, lazy, type ComponentType, type PropsWithChildren } from 'react';
import {
  createBrowserRouter,
  Navigate,
  RouterProvider,
  useLocation,
  useParams,
} from 'react-router-dom';
const ErrorState = lazy(() => import('../components/ui/ErrorState'));
import { AppearanceProvider } from './preferences';
const Providers = lazy(() => import('./Providers'));
const Login = lazy(() => import('../features/Login'));
const Applications = lazy(() => import('../features/Workspace'));
const Workspace = lazy(() =>
  import('../features/Workspace').then((module) => ({ default: module.Workspace })),
);
const Campaigns = lazy(() => import('../features/campaigns/List'));
import { Loading } from '../components/ui/Loading';

const Detail = lazy(() => import('../features/campaigns/Detail'));
const Editor = lazy(() => import('../features/campaigns/Editor'));
const Overview = lazy(() => import('../features/management/Overview'));
const Settings = lazy(() => import('../features/management/Settings'));
const Templates = lazy(() => import('../features/management/Templates'));
const TemplateEditor = lazy(() =>
  import('../features/management/Templates').then((module) => ({ default: module.TemplateEditor })),
);
const Segments = lazy(() => import('../features/management/Segments'));
const SegmentEditor = lazy(() =>
  import('../features/management/Segments').then((module) => ({ default: module.SegmentEditor })),
);
const Audience = lazy(() => import('../features/management/Audience'));
const UserDetail = lazy(() =>
  import('../features/management/Audience').then((module) => ({ default: module.UserDetail })),
);
const Results = lazy(() => import('../features/results/Results'));
const Staff = lazy(() => import('../features/admin/Staff'));
const StaffEditor = lazy(() =>
  import('../features/admin/Staff').then((module) => ({ default: module.StaffEditor })),
);
const Registry = lazy(() => import('../features/admin/Registry'));
const RegistryDetail = lazy(() =>
  import('../features/admin/Registry').then((module) => ({ default: module.RegistryDetail })),
);
const Onboarding = lazy(() => import('../features/admin/Onboarding'));
const Uploads = lazy(() => import('../features/management/Activity'));
const UploadRows = lazy(() =>
  import('../features/management/Activity').then((module) => ({ default: module.UploadRows })),
);
const AuditLog = lazy(() =>
  import('../features/management/Activity').then((module) => ({ default: module.AuditLog })),
);
class RenderBoundary extends Component<PropsWithChildren, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <Suspense fallback={<Loading />}>
        <ErrorState />
      </Suspense>
    ) : (
      this.props.children
    );
  }
}

function Page({ component: View }: { component: ComponentType }) {
  const location = useLocation();
  return <View key={location.pathname} />;
}
function OnboardingRoute() {
  const { managedAppId } = useParams();
  return managedAppId ? <Onboarding key={managedAppId} applicationID={managedAppId} /> : null;
}
const router = createBrowserRouter([
  {
    element: (
      <Suspense fallback={<Loading />}>
        <Providers />
      </Suspense>
    ),
    children: [
      { path: '/', element: <Navigate to="/apps" replace /> },
      { path: '/login', element: <Login /> },
      {
        path: '/apps',
        element: <Workspace />,
        children: [
          { index: true, element: <Applications /> },
          ...[
            ['settings', Settings],
            ['admin/staff', Staff],
            ['admin/staff/new', StaffEditor],
            ['admin/staff/:staffId', StaffEditor],
            ['admin/applications', Registry],
            ['admin/applications/:managedAppId', RegistryDetail],
            [':appId/audit', AuditLog],
            [':appId/uploads', Uploads],
            [':appId/uploads/:batchId', UploadRows],
            [':appId/overview', Overview],
            [':appId/settings', Settings],
            [':appId/templates', Templates],
            [':appId/templates/new', TemplateEditor],
            [':appId/templates/:templateId', TemplateEditor],
            [':appId/segments', Segments],
            [':appId/segments/new', SegmentEditor],
            [':appId/segments/:segmentId', SegmentEditor],
            [':appId/audience', Audience],
            [':appId/audience/:userId', UserDetail],
            [':appId/campaigns', Campaigns],
            [':appId/campaigns/new', Editor],
            [':appId/campaigns/:campaignId', Detail],
            [':appId/campaigns/:campaignId/edit', Editor],
            [':appId/campaigns/:campaignId/results', Results],
          ].map(([path, view]) => ({
            path: path as string,
            element: <Page component={view as ComponentType} />,
          })),
          { path: 'admin/applications/:managedAppId/onboarding', element: <OnboardingRoute /> },
          { path: ':appId/logs', element: <Campaigns results /> },
        ],
      },
      { path: '*', element: <ErrorState notFound /> },
    ],
  },
]);

export default function DashboardApp() {
  return (
    <AppearanceProvider>
      <RenderBoundary>
        <RouterProvider router={router} />
      </RenderBoundary>
    </AppearanceProvider>
  );
}
