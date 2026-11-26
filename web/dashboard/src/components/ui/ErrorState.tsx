import { Center } from '@astryxdesign/core/Center';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Button } from '@astryxdesign/core/Button';
import { usePageFocus } from './usePageFocus';
export default function ErrorState({ notFound = false }: { notFound?: boolean }) {
  usePageFocus();
  return (
    <Center minHeight="100dvh" padding={6}>
      <EmptyState
        headingLevel={1}
        title={notFound ? 'Page not found' : 'Unable to display this page'}
        description={notFound ? undefined : 'Reload the page to try again.'}
        actions={
          notFound ? (
            <Button label="Back to applications" href="/apps" />
          ) : (
            <Button label="Reload" onClick={() => window.location.reload()} />
          )
        }
      />
    </Center>
  );
}
