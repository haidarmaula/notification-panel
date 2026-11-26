import { Suspense } from 'react';
import { Outlet } from 'react-router-dom';
import RouterLink from './RouterLink';
import { QueryClientProvider } from '@tanstack/react-query';
import { LinkProvider } from '@astryxdesign/core/Link';
import { AuthProvider } from './auth';
import { makeQueryClient } from './auth-state';
import { Loading } from '../components/ui/Loading';
import { UnsavedChangesDialog } from '../components/ui/UnsavedChangesDialog';

const client = makeQueryClient();
export default function Providers() {
  return (
    <QueryClientProvider client={client}>
      <AuthProvider>
        <LinkProvider component={RouterLink}>
          <Suspense fallback={<Loading />}>
            <Outlet />
          </Suspense>
          <UnsavedChangesDialog />
        </LinkProvider>
      </AuthProvider>
    </QueryClientProvider>
  );
}
