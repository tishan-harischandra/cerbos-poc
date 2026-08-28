import { Component } from '@angular/core';

/**
 * Shown for the moment between sessionGuard deciding a login is needed
 * and the browser leaving for Keycloak.
 *
 * It exists so that navigation has somewhere to land. A canMatch guard
 * that simply returns false matches no route at all, and with no route
 * matched the router raises NG04002 ("Cannot match any routes") - an
 * error, for what is the ordinary case of not being logged in yet.
 */
@Component({
  standalone: true,
  template: `<p data-testid="signing-in">Redirecting to sign in…</p>`,
})
export class SigningIn {}
