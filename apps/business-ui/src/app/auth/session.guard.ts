import { inject } from '@angular/core';
import { CanMatchFn, Route, Router, UrlSegment } from '@angular/router';

import { AuthService } from './auth.service';
import { ClinicalSession } from './clinical-session';

/**
 * Establishes the session every capability-guarded route depends on:
 * a login first, then the module-level capability snapshot.
 *
 * This runs in canMatch, and the capability check runs in canActivate,
 * because the router completes URL recognition - and so every canMatch
 * guard - before the canActivate phase begins. Guards within a single
 * canMatch array give no such ordering: they are all invoked at once, so
 * a capability check listed after this one would read the store while
 * this guard's fetch was still in flight.
 */
export const sessionGuard: CanMatchFn = (route: Route, segments: UrlSegment[]) => {
  const auth = inject(AuthService);
  if (!auth.isAuthenticated()) {
    void auth.login(`/${segments.map((segment) => segment.path).join('/')}`);
    // A redirect rather than false: returning false matches no route at
    // all and the router reports NG04002 for what is simply a user who
    // has not logged in yet. The browser leaves for Keycloak either way.
    return inject(Router).parseUrl('/signing-in');
  }
  return inject(ClinicalSession)
    .ensureModuleSnapshot()
    .then(() => true);
};
