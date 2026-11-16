import { Button, type ButtonProps } from '@astryxdesign/core/Button';
import { Icon } from '@astryxdesign/core/Icon';
import { ArrowLeftIcon, PlusIcon } from '@heroicons/react/24/outline';

export function CreateAction(props: Omit<ButtonProps, 'variant' | 'icon'>) {
  return <Button {...props} variant="primary" icon={<Icon icon={PlusIcon} size="sm" />} />;
}

export function SubmitAction(props: Omit<ButtonProps, 'type' | 'variant'>) {
  return <Button {...props} type="submit" variant="primary" />;
}

export function BackAction({ label, href }: { label: string; href: string }) {
  return (
    <Button
      label={label}
      href={href}
      variant="ghost"
      size="sm"
      icon={<Icon icon={ArrowLeftIcon} size="sm" />}
    />
  );
}
