import type { ReactNode } from 'react';
import { BackAction } from './Actions';
import { usePageFocus } from './usePageFocus';
import { Layout, LayoutContent, LayoutHeader, LayoutFooter } from '@astryxdesign/core/Layout';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';

export function PageFrame({
  title,
  description,
  back,
  actions,
  children,
  contentWidth,
  footer,
}: {
  title: ReactNode;
  description?: ReactNode;
  back?: { label: string; href: string };
  actions?: ReactNode;
  children: ReactNode;
  contentWidth?: number;
  footer?: ReactNode;
}) {
  usePageFocus();
  return (
    <Layout
      padding={6}
      footer={
        footer ? (
          <LayoutFooter hasDivider role="region" label="Page footer">
            {footer}
          </LayoutFooter>
        ) : undefined
      }
      header={
        <LayoutHeader role="region" label="Page header">
          <VStack gap={4}>
            {back && (
              <HStack>
                <BackAction label={back.label} href={back.href} />
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
          <VStack gap={6} width="100%" maxWidth={contentWidth}>
            {children}
          </VStack>
        </LayoutContent>
      }
    />
  );
}
