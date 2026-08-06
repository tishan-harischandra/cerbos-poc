import { Component } from '@angular/core';

/**
 * Forbidden is where capabilityGuard redirects a route match that the
 * capability snapshot denies (§12.5). It is a UX control only: the
 * backend independently enforces every operation regardless of whether
 * the browser ever reaches this page.
 */
@Component({
  standalone: true,
  template: `<h1 data-testid="forbidden">You do not have access to this page.</h1>`,
})
export class Forbidden {}
