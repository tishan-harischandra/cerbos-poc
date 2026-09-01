import { Component, OnInit, inject, signal } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';

import { AuthService } from './auth.service';

/**
 * The route Keycloak redirects back to after login. Exchanges the
 * authorization code for a token and moves on to the shell, or shows a
 * plain retry prompt on failure - never a stack trace, since a stale
 * bookmark to this URL or a replayed callback are both routine.
 */
@Component({
  standalone: true,
  template: `
    @if (failed()) {
      <p data-testid="callback-failed">
        Login could not be completed. <button type="button" (click)="retry()">Try again</button>
      </p>
    } @else {
      <p data-testid="callback-pending">Completing login…</p>
    }
  `,
})
export class Callback implements OnInit {
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly auth = inject(AuthService);

  protected readonly failed = signal(false);

  async ngOnInit(): Promise<void> {
    const params = this.route.snapshot.queryParamMap;
    const code = params.get('code');
    const state = params.get('state');

    if (!code || !state) {
      this.failed.set(true);
      return;
    }

    const ok = await this.auth.handleCallback(code, state);
    if (!ok) {
      this.failed.set(true);
      return;
    }
    // A deep link the auth guard redirected away from survives here
    // (issue #82); a login started from nowhere in particular - the
    // login button, a stale bookmark to /callback - falls back to the
    // shell's default route.
    await this.router.navigateByUrl(this.auth.consumeReturnTo() ?? '/');
  }

  retry(): void {
    void this.auth.login();
  }
}
