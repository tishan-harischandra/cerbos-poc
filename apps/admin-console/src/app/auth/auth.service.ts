import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import {
  HospitalSwitcher,
  TokenClaims,
  decodeAccessToken,
  deriveCodeChallenge,
  generateCodeVerifier,
  generateState,
} from '@cerbos-poc/auth';

import { OIDC_CONFIG } from './oidc-config';
import { REDIRECT } from './redirect';

const VERIFIER_KEY = 'admin-console:pkce-verifier';
const STATE_KEY = 'admin-console:pkce-state';
const RETURN_TO_KEY = 'admin-console:return-to';

/**
 * The Admin Console's OIDC login (§9's "Admin Console shell, navigation
 * and OIDC login", issue #16), an Authorization Code + PKCE flow against
 * Keycloak.
 *
 * The access token lives only in this service's own in-memory signal,
 * never in localStorage or sessionStorage - only the ephemeral PKCE
 * verifier and state survive the redirect round trip, in sessionStorage,
 * and both are deleted the moment the callback consumes them. Every
 * administrative call still goes through the server (§16.1); this token
 * only authenticates the browser to the Administration Service and the
 * ADS, never to the identity provider's own admin API.
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly config = inject(OIDC_CONFIG);
  private readonly redirect = inject(REDIRECT);
  private readonly hospitalSwitcher = inject(HospitalSwitcher);

  private readonly accessTokenSignal = signal<string | null>(null);
  private readonly claimsSignal = signal<TokenClaims | null>(null);

  readonly isAuthenticated = computed(() => this.accessTokenSignal() !== null);
  readonly claims = this.claimsSignal.asReadonly();

  /**
   * Keycloak's own administration console for this realm (issue #82): the
   * two consoles are clients of the same realm sharing one SSO session, so
   * this is a plain link, not a second login.
   */
  readonly keycloakConsoleUrl = computed(() => {
    const issuerUrl = new URL(this.config.issuer);
    const realm = issuerUrl.pathname.replace(/^\/realms\//, '');
    return `${issuerUrl.origin}/admin/${realm}/console/`;
  });

  accessToken(): string | null {
    return this.accessTokenSignal();
  }

  /**
   * Redirects the browser to Keycloak's authorization endpoint. returnTo,
   * when given, is the in-app path to land on once login completes - a
   * deep link into the console must survive the round trip rather than
   * always landing on the shell's default route (issue #82).
   */
  async login(returnTo?: string): Promise<void> {
    const verifier = generateCodeVerifier();
    const state = generateState();
    const challenge = await deriveCodeChallenge(verifier);

    sessionStorage.setItem(VERIFIER_KEY, verifier);
    sessionStorage.setItem(STATE_KEY, state);
    if (returnTo) {
      sessionStorage.setItem(RETURN_TO_KEY, returnTo);
    }

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
      this.setAccessToken(response.access_token);
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Reads and clears the path stashed by {@link login}, if any - the deep
   * link a guarded route redirected away from. Read once: a stale value
   * from an abandoned login attempt must not resurface on a later one.
   */
  consumeReturnTo(): string | null {
    const returnTo = sessionStorage.getItem(RETURN_TO_KEY);
    sessionStorage.removeItem(RETURN_TO_KEY);
    return returnTo;
  }

  /**
   * Switches to a different hospital with no re-entry of credentials
   * (issue #84): a fresh authorization request against the browser's
   * existing SSO session, scoped to organization. Returns false, leaving
   * whatever token was already active untouched, when the user does not
   * belong to that organization or the silent request otherwise cannot
   * be satisfied - the caller sees no partial or inconsistent state
   * either way.
   */
  async switchHospital(organization: string): Promise<boolean> {
    try {
      const token = await this.hospitalSwitcher.switchTo(this.config, organization);
      this.setAccessToken(token);
      return true;
    } catch {
      return false;
    }
  }

  logout(): void {
    this.accessTokenSignal.set(null);
    this.claimsSignal.set(null);
    const params = new URLSearchParams({
      client_id: this.config.clientId,
      post_logout_redirect_uri: window.location.origin,
    });
    this.redirect(`${this.config.issuer}/protocol/openid-connect/logout?${params}`);
  }

  private setAccessToken(token: string): void {
    this.accessTokenSignal.set(token);
    this.claimsSignal.set(decodeAccessToken(token, this.config.clientId));
  }
}
