import { inject } from '@angular/core';
import { CanActivateFn, CanMatchFn, Router, UrlTree } from '@angular/router';

import { CapabilityStore } from './capability-store';

/**
 * decide is the one capability check both guards below share: a
 * synchronous read of the store, and /forbidden if it does not grant.
 * Must be called from an injection context.
 */
function decide(key: string): true | UrlTree {
  const capabilities = inject(CapabilityStore);
  const router = inject(Router);
  return capabilities.can(key) ? true : router.parseUrl('/forbidden');
}

/**
 * capabilityGuard is a UX control only (§12.5, §12.6): it reads the
 * CapabilityStore's signal synchronously and issues no network call per
 * navigation. The route it protects is still, independently, enforced by
 * its owning backend endpoint - a modified or bypassed guard grants
 * nothing (§16.1).
 *
 * Use this only where the snapshot is already loaded by the time the
 * router recognises the URL. If loading it is itself part of the
 * navigation, use capabilityCanActivate instead - see why below.
 */
export const capabilityGuard: CanMatchFn = (route) =>
  decide(route.data?.['capability'] as string);

/**
 * capabilityCanActivate is the same check, run in the canActivate phase.
 *
 * It exists because a route cannot order this check after an asynchronous
 * guard that loads the snapshot: the router invokes every canMatch guard
 * on a route eagerly and concurrently, then picks the first non-true
 * result by position (runCanMatchGuards -> prioritizedGuardValue). So a
 * capability check listed second in canMatch still reads the store before
 * the guard listed first has resolved, sees nothing, and redirects to
 * /forbidden.
 *
 * canMatch is a different matter: it completes during URL recognition,
 * which finishes before the canActivate phase begins. Loading the
 * snapshot in canMatch and checking it here is what actually sequences
 * the two.
 */
export const capabilityCanActivate: CanActivateFn = (route) =>
  decide(route.data?.['capability'] as string);
