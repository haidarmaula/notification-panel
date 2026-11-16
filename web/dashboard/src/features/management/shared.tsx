import type { ReactNode } from 'react';
import { HStack } from '@astryxdesign/core/Stack';
import { Text } from '@astryxdesign/core/Text';
import { Button } from '@astryxdesign/core/Button';
import { Banner } from '@astryxdesign/core/Banner';
import type { usePage, useWrite } from './data-hooks';
import { Failure, Loading } from '../../components/ui/Feedback';
export function Pager<T>({ page }: { page: ReturnType<typeof usePage<T>> }) {
  if (
    page.query.isPending ||
    page.query.isError ||
    (!page.query.data?.items.length && !page.cursors().length)
  )
    return null;
  return (
    <HStack gap={3} wrap="wrap" vAlign="center" hAlign="between">
      <Text>Page {page.cursors().length + 1}</Text>
      <HStack gap={2}>
        <Button
          label="Previous"
          size="sm"
          isDisabled={!page.cursors().length || page.query.isFetching}
          onClick={() => page.setCursors((current) => current.slice(0, -1))}
        />
        <Button
          label="Next"
          size="sm"
          isDisabled={!page.query.data?.next_after_id || page.query.isFetching}
          onClick={() => {
            const cursor = page.query.data?.next_after_id;
            if (cursor) page.setCursors((current) => [...current, cursor]);
          }}
        />
      </HStack>
    </HStack>
  );
}
export function QueryState({
  query,
  children,
  loadingVariant = 'rows',
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown };
  children: ReactNode;
  loadingVariant?: 'rows' | 'form';
}) {
  if (query.isPending) return <Loading variant={loadingVariant} />;
  if (query.isError) return <Failure error={query.error} retry={() => void query.refetch()} />;
  return children;
}
export function WriteFeedback({ operation }: { operation: ReturnType<typeof useWrite> }) {
  return (
    <>
      {operation.error() && (
        <Banner status="error" title="Unable to save changes" description={operation.error()} />
      )}
      {operation.uncertain() && (
        <Banner
          status="warning"
          title="Check the outcome"
          description="The outcome is uncertain. Check the latest data before trying again. This form is blocked to prevent duplicate changes."
        />
      )}
    </>
  );
}
