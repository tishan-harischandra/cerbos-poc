import { inject } from '@angular/core';
import { CanMatchFn, Router } from '@angular/router';

import { CapabilityStore } from './capability-store';

/**
 * capabilityGuard is a UX control only (§12.5, §12.6): it reads the
 * CapabilityStore's signal synchronously and issues no network call per
 * navigation. The route it protects is still, independently, enforced by
 * its owning backend endpoint - a modified or bypassed guard grants
 * nothing (§16.1).
 */
export const capabilityGuard: CanMatchFn = (route) => {
  const capabilities = inject(CapabilityStore);
  const router = inject(Router);
  const key = route.data?.['capability'] as string;
  return capabilities.can(key) ? true : router.parseUrl('/forbidden');
};
