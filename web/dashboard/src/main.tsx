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
const Detail = lazy(() => import('./features/campaigns/Detail'));
const Editor = lazy(() => import('./features/campaigns/Editor'));
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
const DetailRoute = keyed(Detail);
const EditorRoute = keyed(Editor);
const ResultsRoute = keyed(Results);
const root = document.getElementById('root');
if (!root) throw new Error('Missing root');
render(
  () => (
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
      <QueryClientProvider client={makeQueryClient()}>
        <AuthProvider>
          <Router root={(props) => <Suspense fallback={<Loading />}>{props.children}</Suspense>}>
            <Route path="/" component={() => <Navigate href="/apps" />} />
            <Route path="/login" component={Login} />
            <Route path="/apps" component={Workspace}>
              <Route path="/" component={ApplicationRoute} />
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
  ),
  root,
);
