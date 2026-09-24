import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import {
  TokenClaims,
  decodeAccessToken,
  deriveCodeChallenge,
  generateCodeVerifier,
  generateState,
} from '@cerbos-poc/auth';

import { OIDC_CONFIG } from './oidc-config';
import { REDIRECT } from './redirect';

const VERIFIER_KEY = 'business-ui:pkce-verifier';
const STATE_KEY = 'business-ui:pkce-state';
const TRANSITION_KEY = 'business-ui:oidc-transition';
const SWITCH_TARGET_KEY = 'business-ui:switch-target';
const RETURN_TO_KEY = 'business-ui:return-to';

type TransitionKind = 'login' | 'hospital-switch';

/**
 * The Business UI's OIDC login, an Authorization Code + PKCE flow against
 * Keycloak.
 *
 * The access token lives only in this service's own in-memory signal,
 * never in localStorage or sessionStorage. Only ephemeral PKCE and transition
 * metadata survive a redirect round trip, and the callback consumes them once.
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
  private readonly claimsSignal = signal<TokenClaims | null>(null);

  readonly isAuthenticated = computed(() => this.accessTokenSignal() !== null);
  readonly claims = this.claimsSignal.asReadonly();

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
    sessionStorage.setItem(TRANSITION_KEY, 'login');
    sessionStorage.removeItem(SWITCH_TARGET_KEY);
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
    this.redirect(
      `${this.config.issuer}/protocol/openid-connect/auth?${params}`,
    );
  }

  /**
   * Completes the flow after Keycloak redirects back to /callback with a
   * code and the state this session started with. Returns false - and
   * leaves the caller unauthenticated - for a state mismatch (a forged or
   * replayed callback) rather than throwing, so the callback route can
   * show a plain "log in again" prompt instead of an error page.
   */
  async handleCallback(
    code: string | null,
    state: string | null,
    error: string | null = null,
  ): Promise<boolean> {
    const expectedState = sessionStorage.getItem(STATE_KEY);
    const verifier = sessionStorage.getItem(VERIFIER_KEY);
    const transition = sessionStorage.getItem(
      TRANSITION_KEY,
    ) as TransitionKind | null;
    const switchTarget = sessionStorage.getItem(SWITCH_TARGET_KEY);
    sessionStorage.removeItem(STATE_KEY);
    sessionStorage.removeItem(VERIFIER_KEY);
    sessionStorage.removeItem(TRANSITION_KEY);
    sessionStorage.removeItem(SWITCH_TARGET_KEY);

    if (
      error ||
      !code ||
      !state ||
      !expectedState ||
      !verifier ||
      state !== expectedState ||
      (transition !== 'login' && transition !== 'hospital-switch')
    ) {
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
      const claims = decodeAccessToken(
        response.access_token,
        this.config.clientId,
      );
      if (
        transition === 'hospital-switch' &&
        (!switchTarget || claims.hospitalId !== switchTarget)
      ) {
        return false;
      }
      this.setAccessToken(response.access_token, claims);
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

  /**
   * Starts a top-level Authorization Code + PKCE transition for another
   * hospital. Only verifier, state, target and return path survive the
   * navigation; the callback validates the issued token before committing it.
   */
  async switchHospital(organization: string): Promise<void> {
    const verifier = generateCodeVerifier();
    const state = generateState();
    const challenge = await deriveCodeChallenge(verifier);

    sessionStorage.setItem(VERIFIER_KEY, verifier);
    sessionStorage.setItem(STATE_KEY, state);
    sessionStorage.setItem(TRANSITION_KEY, 'hospital-switch');
    sessionStorage.setItem(SWITCH_TARGET_KEY, organization);
    sessionStorage.setItem(
      RETURN_TO_KEY,
      `${window.location.pathname}${window.location.search}${window.location.hash}`,
    );

    const params = new URLSearchParams({
      response_type: 'code',
      client_id: this.config.clientId,
      redirect_uri: this.config.redirectUri,
      scope: `openid organization:${organization}`,
      state,
      code_challenge: challenge,
      code_challenge_method: 'S256',
      prompt: 'none',
    });
    this.redirect(
      `${this.config.issuer}/protocol/openid-connect/auth?${params}`,
    );
  }

  logout(): void {
    this.accessTokenSignal.set(null);
    this.claimsSignal.set(null);
    const params = new URLSearchParams({
      client_id: this.config.clientId,
      post_logout_redirect_uri: window.location.origin,
    });
    this.redirect(
      `${this.config.issuer}/protocol/openid-connect/logout?${params}`,
    );
  }

  private setAccessToken(
    token: string,
    claims = decodeAccessToken(token, this.config.clientId),
  ): void {
    this.accessTokenSignal.set(token);
    this.claimsSignal.set(claims);
  }
}
