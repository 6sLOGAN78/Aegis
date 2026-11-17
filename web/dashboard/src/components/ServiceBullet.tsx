import React from 'react';

// One route colour per backend service, like line bullets on a transit map.
// Colour is never the only cue: the bullet carries a letter and sits next to the service name.
const ROUTES: Record<string, { letter: string; color: string }> = {
  orders: { letter: 'O', color: 'var(--color-route-orders)' },
  payments: { letter: 'P', color: 'var(--color-route-payments)' },
  admin: { letter: 'A', color: 'var(--color-route-admin)' },
};

export function routeFor(serviceId: string) {
  return ROUTES[serviceId] ?? { letter: (serviceId[0] || '?').toUpperCase(), color: 'var(--color-charcoal)' };
}

interface ServiceBulletProps {
  service: string;
  size?: 'md' | 'lg';
  withName?: boolean;
}

export const ServiceBullet: React.FC<ServiceBulletProps> = ({ service, size = 'md', withName = true }) => {
  const r = routeFor(service);
  return (
    <span className="inline-flex items-center gap-2 min-w-0">
      <span className={`route ${size === 'lg' ? 'route-lg' : ''}`} style={{ background: r.color }} aria-hidden="true">
        {r.letter}
      </span>
      {withName ? <span className="truncate">{service}</span> : <span className="sr-only">{service}</span>}
    </span>
  );
};
