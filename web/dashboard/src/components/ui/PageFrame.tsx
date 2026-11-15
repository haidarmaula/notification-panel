import type { ReactNode } from 'react';
import { ArrowLeftIcon } from '@heroicons/react/24/outline';
import { Button } from '@astryxdesign/core/Button';
import { Icon } from '@astryxdesign/core/Icon';
import { Layout, LayoutContent, LayoutHeader } from '@astryxdesign/core/Layout';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';

export function PageFrame({
  title,
  description,
  back,
  actions,
  children,
  contentWidth,
}: {
  title: ReactNode;
  description?: ReactNode;
  back?: { label: string; href: string };
  actions?: ReactNode;
  children: ReactNode;
  contentWidth?: number;
}) {
  return (
    <Layout
      padding={6}
      contentWidth={contentWidth}
      header={
        <LayoutHeader>
          <VStack gap={4}>
            {back && (
              <HStack>
                <Button
                  label={back.label}
                  href={back.href}
                  variant="ghost"
                  size="sm"
                  icon={<Icon icon={ArrowLeftIcon} size="sm" />}
                />
              </HStack>
            )}
            <HStack gap={4} wrap="wrap" hAlign="between" vAlign="center">
              <VStack gap={1}>
                <Heading level={1}>{title}</Heading>
                {description && <Text color="secondary">{description}</Text>}
              </VStack>
              {actions && (
                <HStack gap={2} wrap="wrap">
                  {actions}
                </HStack>
              )}
            </HStack>
          </VStack>
        </LayoutHeader>
      }
      content={
        <LayoutContent>
          <VStack gap={6}>{children}</VStack>
        </LayoutContent>
      }
    />
  );
}
