import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { OIDC_CONFIG } from './oidc-config';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';
import { REDIRECT } from './redirect';

const VERIFIER_KEY = 'business-ui:pkce-verifier';
const STATE_KEY = 'business-ui:pkce-state';
const RETURN_TO_KEY = 'business-ui:return-to';

/**
 * The Business UI's OIDC login, an Authorization Code + PKCE flow against
 * Keycloak.
 *
 * The access token lives only in this service's own in-memory signal,
 * never in localStorage or sessionStorage - only the ephemeral PKCE
 * verifier, the state and the URL the user was heading for survive the
 * redirect round trip, in sessionStorage, and all three are deleted the
 * moment the callback consumes them.
 *
 * The token authenticates the browser to the ADS and nothing else. It
 * carries no administrative permission (§7.1), and the capability
 * snapshot it fetches is a UX control only: every route it unlocks is
 * still independently enforced by the endpoint behind it (§16.1).
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly config = inject(OIDC_CONFIG);
  private readonly redirect = inject(REDIRECT);

  private readonly accessTokenSignal = signal<string | null>(null);

  readonly isAuthenticated = computed(() => this.accessTokenSignal() !== null);

  accessToken(): string | null {
    return this.accessTokenSignal();
  }

  /**
   * Redirects the browser to Keycloak's authorization endpoint,
   * remembering where the user was going so the callback can put them
   * back there rather than always landing them on the root.
   */
  async login(returnTo?: string): Promise<void> {
    const verifier = generateCodeVerifier();
    const state = generateState();
    const challenge = await deriveCodeChallenge(verifier);

    sessionStorage.setItem(VERIFIER_KEY, verifier);
    sessionStorage.setItem(STATE_KEY, state);
    if (returnTo) sessionStorage.setItem(RETURN_TO_KEY, returnTo);

    const params = new URLSearchParams({
      response_type: 'code',
      client_id: this.config.clientId,
      redirect_uri: this.config.redirectUri,
      scope: 'openid',
      state,
      code_challenge: challenge,
      code_challenge_method: 'S256',
    });
    this.redirect(`${this.config.issuer}/protocol/openid-connect/auth?${params}`);
  }

  /**
   * Completes the flow after Keycloak redirects back to /callback with a
   * code and the state this session started with. Returns false - and
   * leaves the caller unauthenticated - for a state mismatch (a forged or
   * replayed callback) rather than throwing, so the callback route can
   * show a plain "log in again" prompt instead of an error page.
   */
  async handleCallback(code: string, state: string): Promise<boolean> {
    const expectedState = sessionStorage.getItem(STATE_KEY);
    const verifier = sessionStorage.getItem(VERIFIER_KEY);
    sessionStorage.removeItem(STATE_KEY);
    sessionStorage.removeItem(VERIFIER_KEY);

    if (!expectedState || !verifier || state !== expectedState) {
      return false;
    }

    const body = new URLSearchParams({
      grant_type: 'authorization_code',
      client_id: this.config.clientId,
      redirect_uri: this.config.redirectUri,
      code,
      code_verifier: verifier,
    });

    try {
      const response = await firstValueFrom(
        this.http.post<{ access_token: string }>(
          `${this.config.issuer}/protocol/openid-connect/token`,
          body.toString(),
          { headers: { 'Content-Type': 'application/x-www-form-urlencoded' } },
        ),
      );
      this.accessTokenSignal.set(response.access_token);
      return true;
    } catch {
      return false;
    }
  }

  /** Where the user was heading before login, consumed once. */
  takeReturnTo(): string {
    const returnTo = sessionStorage.getItem(RETURN_TO_KEY);
    sessionStorage.removeItem(RETURN_TO_KEY);
    return returnTo || '/';
  }

  logout(): void {
    this.accessTokenSignal.set(null);
    const params = new URLSearchParams({
      client_id: this.config.clientId,
      post_logout_redirect_uri: window.location.origin,
    });
    this.redirect(`${this.config.issuer}/protocol/openid-connect/logout?${params}`);
  }
}
