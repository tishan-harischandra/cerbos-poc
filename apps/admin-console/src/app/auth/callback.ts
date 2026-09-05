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
    <div class="callback">
      <div class="callback__card">
        <span class="callback__brand">GrantPlane</span>
        @if (failed()) {
          <p class="error" role="alert" data-testid="callback-failed">
            Login could not be completed.
            <button class="btn-primary" type="button" (click)="retry()">Try again</button>
          </p>
        } @else {
          <p class="muted" role="status" data-testid="callback-pending">Completing login…</p>
        }
      </div>
    </div>
  `,
  styles: `
    .callback {
      display: grid;
      place-items: center;
      min-height: 100vh;
      padding: var(--gp-space-4);
    }

    .callback__card {
      display: flex;
      flex-direction: column;
      gap: var(--gp-space-3);
      align-items: center;
      min-width: 18rem;
      padding: var(--gp-space-6) var(--gp-space-5);
      border: 1px solid var(--gp-border);
      border-radius: var(--gp-radius-lg);
      background: var(--gp-surface);
      box-shadow: var(--gp-shadow);
      text-align: center;
    }

    .callback__brand {
      font-size: var(--gp-text-xl);
      font-weight: 700;
      letter-spacing: -0.01em;
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
