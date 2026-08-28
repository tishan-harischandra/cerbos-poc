import { Provider } from '@angular/core';

import { AuthService } from './auth.service';
import { ClinicalSession } from './clinical-session';

/**
 * Test providers standing in for a session that is already established:
 * a logged-in user whose module snapshot has already been loaded.
 *
 * Specs about capability routing are not specs about logging in. Without
 * this, every one of them would have to drive a PKCE redirect and flush a
 * snapshot fetch before reaching the behaviour it actually asserts on -
 * and would fail for the wrong reason if the login flow changed.
 * session.guard.spec.ts covers the guard itself, with the real thing.
 */
export function provideEstablishedSession(): Provider[] {
  return [
    {
      provide: AuthService,
      useValue: {
        isAuthenticated: () => true,
        accessToken: () => 'a-token',
        login: () => Promise.resolve(),
        takeReturnTo: () => '/',
        logout: () => undefined,
      },
    },
    {
      provide: ClinicalSession,
      useValue: {
        ensureModuleSnapshot: () => Promise.resolve(),
        reset: () => undefined,
      },
    },
  ];
}
