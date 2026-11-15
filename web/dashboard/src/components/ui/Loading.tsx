import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Skeleton } from '@astryxdesign/core/Skeleton';
import { VisuallyHidden } from '@astryxdesign/core/VisuallyHidden';

export function Loading({
  label = 'Loading data…',
  rows = 3,
  variant = 'rows',
}: {
  label?: string;
  rows?: number;
  variant?: 'rows' | 'form';
}) {
  return (
    <VStack gap={4} role="status">
      <VisuallyHidden>{label}</VisuallyHidden>
      {Array.from({ length: rows }, (_, index) =>
        variant === 'form' ? (
          <VStack key={index} gap={2}>
            <Skeleton width="25%" height="1em" index={index} />
            <Skeleton width="100%" height="2em" index={index} />
          </VStack>
        ) : (
          <HStack key={index} gap={6}>
            <Skeleton width="50%" height="1em" index={index} />
            <Skeleton width="25%" height="1em" index={index} />
            <Skeleton width="25%" height="1em" index={index} />
          </HStack>
        ),
      )}
    </VStack>
  );
}
