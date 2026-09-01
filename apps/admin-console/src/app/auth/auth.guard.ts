import { inject } from '@angular/core';
import { CanActivateFn } from '@angular/router';

import { AuthService } from './auth.service';

/**
 * Refuses navigation to any route it guards until AuthService has an
 * access token, sending the browser to Keycloak instead. Every route but
 * /callback is guarded (§9's "OIDC login").
 *
 * The requested URL travels with the redirect (issue #82): a deep link
 * into the console must land on the exact path requested once login
 * completes, not always on the shell's default route.
 */
export const authGuard: CanActivateFn = (_route, state) => {
  const auth = inject(AuthService);
  if (auth.isAuthenticated()) {
    return true;
  }
  void auth.login(state.url);
  return false;
};
