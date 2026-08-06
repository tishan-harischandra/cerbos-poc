import { inject } from '@angular/core';
import { CanActivateFn } from '@angular/router';

import { AuthService } from './auth.service';

/**
 * Refuses navigation to any route it guards until AuthService has an
 * access token, sending the browser to Keycloak instead. Every route but
 * /callback is guarded (§9's "OIDC login").
 */
export const authGuard: CanActivateFn = () => {
  const auth = inject(AuthService);
  if (auth.isAuthenticated()) {
    return true;
  }
  void auth.login();
  return false;
};
