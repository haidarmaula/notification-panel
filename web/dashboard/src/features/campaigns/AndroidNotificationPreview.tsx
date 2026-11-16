import {
  Battery50Icon,
  BellIcon,
  ChevronUpIcon,
  SignalIcon,
  WifiIcon,
} from '@heroicons/react/24/outline';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Text } from '@astryxdesign/core/Text';
import styles from './AndroidNotificationPreview.module.css';

type AndroidNotificationPreviewProps = {
  applicationName: string;
  title: string;
  body: string;
  context?: 'campaign' | 'template' | 'review';
};

export function AndroidNotificationPreview({
  applicationName,
  title,
  body,
  context = 'campaign',
}: AndroidNotificationPreviewProps) {
  return (
    <VStack gap={6} paddingBlockStart={4} width="100%">
      <section className={styles.device} aria-label="Android notification preview">
        <header className={styles.statusBar} aria-label="Android status bar">
          <span className={styles.time}>9:32</span>
          <span className={styles.camera} aria-hidden="true" />
          <span className={styles.statusIcons} aria-label="Wi-Fi, mobile signal, battery">
            <Icon icon={WifiIcon} size="sm" />
            <Icon icon={SignalIcon} size="sm" />
            <Icon icon={Battery50Icon} size="sm" />
          </span>
        </header>
        <article className={styles.notification} aria-label="Notification">
          <header className={styles.notificationHeader}>
            <span className={styles.appIcon} aria-hidden="true">
              <Icon icon={BellIcon} size="sm" />
            </span>
            <span className={styles.appLabel}>{applicationName} · now</span>
            <Icon icon={ChevronUpIcon} size="sm" />
          </header>
          <VStack gap={1}>
            <h3 className={styles.notificationTitle}>{title || 'Your notification title'}</h3>
            <p className={styles.notificationBody}>
              {body || 'Your message will appear here as you type.'}
            </p>
          </VStack>
        </article>
      </section>
      <HStack>
        <Text type="supporting" color="secondary">
          Google Android · Native app
        </Text>
      </HStack>
      <Text color="secondary">
        The final appearance depends on the recipient’s device.
        {context === 'campaign' &&
          ' Saving a draft does not send a notification; you will review it again before sending.'}
        {context === 'template' &&
          ' This template fills a draft; it does not send a notification. Review the message before sending.'}
      </Text>
    </VStack>
  );
}
