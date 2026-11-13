import { render } from 'solid-js/web';
import { lazy, ErrorBoundary, Suspense, Show, type Component } from 'solid-js';
import { Router, Route, Navigate, useLocation } from '@solidjs/router';
import { QueryClientProvider } from '@tanstack/solid-query';
import { AuthProvider, makeQueryClient } from './app/auth';
import Login from './features/Login';
import Applications, { Workspace } from './features/Workspace';
import Campaigns from './features/campaigns/List';
import { Loading } from './components/ui/Feedback';
import './styles.css';
import { initAppearance } from './app/preferences';
const Detail = lazy(() => import('./features/campaigns/Detail'));
const Editor = lazy(() => import('./features/campaigns/Editor'));
const Overview = lazy(() => import('./features/management/Overview'));
const Settings = lazy(() => import('./features/management/Settings'));
const Templates = lazy(() => import('./features/management/Templates'));
const TemplateEditor = lazy(() =>
  import('./features/management/Templates').then((module) => ({ default: module.TemplateEditor })),
);
const Segments = lazy(() => import('./features/management/Segments'));
const SegmentEditor = lazy(() =>
  import('./features/management/Segments').then((module) => ({ default: module.SegmentEditor })),
);
const Audience = lazy(() => import('./features/management/Audience'));
const UserDetail = lazy(() =>
  import('./features/management/Audience').then((module) => ({ default: module.UserDetail })),
);
const Results = lazy(() => import('./features/results/Results'));
function keyed(Page: Component): Component {
  return () => {
    const location = useLocation();
    return (
      <Show when={location.pathname} keyed>
        {(identity) => identity && <Page />}
      </Show>
    );
  };
}
const ApplicationRoute = keyed(Applications);
const CampaignRoute = keyed(Campaigns);
const LogsRoute = keyed(() => <Campaigns results />);
const DetailRoute = keyed(Detail);
const EditorRoute = keyed(Editor);
const ResultsRoute = keyed(Results);
const OverviewRoute = keyed(Overview);
const SettingsRoute = keyed(Settings);
const TemplatesRoute = keyed(Templates);
const TemplateEditorRoute = keyed(TemplateEditor);
const SegmentsRoute = keyed(Segments);
const SegmentEditorRoute = keyed(SegmentEditor);
const AudienceRoute = keyed(Audience);
const UserDetailRoute = keyed(UserDetail);
const root = document.getElementById('root');
if (!root) throw new Error('Missing root');
render(() => {
  initAppearance();
  const client = makeQueryClient();
  return (
    <ErrorBoundary
      fallback={
        <main class="fatal-error">
          <h1>Halaman belum dapat ditampilkan.</h1>
          <p>Muat ulang halaman untuk mencoba kembali.</p>
          <button class="button primary" onClick={() => window.location.reload()}>
            Muat ulang
          </button>
        </main>
      }
    >
      <QueryClientProvider client={client}>
        <AuthProvider>
          <Router root={(props) => <Suspense fallback={<Loading />}>{props.children}</Suspense>}>
            <Route path="/" component={() => <Navigate href="/apps" />} />
            <Route path="/login" component={Login} />
            <Route path="/apps" component={Workspace}>
              <Route path="/" component={ApplicationRoute} />
              <Route path="/:appId/logs" component={LogsRoute} />
              <Route path="/:appId/overview" component={OverviewRoute} />
              <Route path="/:appId/settings" component={SettingsRoute} />
              <Route path="/:appId/templates" component={TemplatesRoute} />
              <Route path="/:appId/templates/new" component={TemplateEditorRoute} />
              <Route path="/:appId/templates/:templateId" component={TemplateEditorRoute} />
              <Route path="/:appId/segments" component={SegmentsRoute} />
              <Route path="/:appId/segments/new" component={SegmentEditorRoute} />
              <Route path="/:appId/segments/:segmentId" component={SegmentEditorRoute} />
              <Route path="/:appId/audience" component={AudienceRoute} />
              <Route path="/:appId/audience/:userId" component={UserDetailRoute} />
              <Route path="/:appId/campaigns" component={CampaignRoute} />
              <Route path="/:appId/campaigns/new" component={EditorRoute} />
              <Route path="/:appId/campaigns/:campaignId" component={DetailRoute} />
              <Route path="/:appId/campaigns/:campaignId/edit" component={EditorRoute} />
              <Route path="/:appId/campaigns/:campaignId/results" component={ResultsRoute} />
            </Route>
            <Route
              path="*"
              component={() => (
                <main class="empty-state">
                  <h1>Halaman tidak ditemukan.</h1>
                  <a class="button secondary" href="/apps">
                    Kembali ke aplikasi
                  </a>
                </main>
              )}
            />
          </Router>
        </AuthProvider>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}, root);
