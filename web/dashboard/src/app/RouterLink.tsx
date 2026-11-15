import type { ComponentProps } from 'react';
import { Link } from 'react-router-dom';

type RouterLinkProps = Omit<ComponentProps<typeof Link>, 'to'> & {
  href?: string;
  to?: ComponentProps<typeof Link>['to'];
};
export default function RouterLink({ href, to, ...props }: RouterLinkProps) {
  const destination = to ?? href ?? '/apps';
  const path = typeof destination === 'string' ? destination : (destination.pathname ?? '');
  const isApplicationRoute = path === '/login' || path === '/apps' || path.startsWith('/apps/');
  return (
    <Link
      {...props}
      to={destination}
      reloadDocument={props.reloadDocument || !isApplicationRoute}
    />
  );
}
