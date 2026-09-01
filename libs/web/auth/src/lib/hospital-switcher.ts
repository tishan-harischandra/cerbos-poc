import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { OidcClient } from './oidc-client';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';
import { SILENT_FRAME } from './silent-frame';

/**
 * Requests a token scoped to a different organization against the
 * browser's existing SSO session, with no re-entry of credentials (issue
 * #84, PRD §"hospital switching is a fresh authorization request naming
 * the target alias, made silently against the existing SSO session").
 *
 * This is the one place either console asks Keycloak for a hospital
 * switch - the shared web library issue #84 asks for, replacing what
 * would otherwise be a second copy of the login flow's own PKCE exchange
 * per application.
 */
@Injectable({ providedIn: 'root' })
export class HospitalSwitcher {
  private readonly http = inject(HttpClient);
  private readonly silentFrame = inject(SILENT_FRAME);

  /**
   * Resolves with the new access token on success. Rejects - leaving
   * whatever token the caller already holds untouched, since nothing here
   * mutates caller state - when the organization is not one the user
   * belongs to, or the silent request otherwise cannot be satisfied
   * without an interactive screen (issue #84's "a switch to an
   * organization the user is not a member of fails and leaves the
   * existing session intact").
   */
  async switchTo(client: OidcClient, organization: string): Promise<string> {
    const verifier = generateCodeVerifier();
    const state = generateState();
    const challenge = await deriveCodeChallenge(verifier);

    const params = new URLSearchParams({
      response_type: 'code',
      client_id: client.clientId,
      redirect_uri: client.redirectUri,
      scope: `openid organization:${organization}`,
      state,
      code_challenge: challenge,
      code_challenge_method: 'S256',
      prompt: 'none',
    });

    const redirectedTo = await this.silentFrame(
      `${client.issuer}/protocol/openid-connect/auth?${params}`,
      client.redirectUri,
    );

    const returned = new URL(redirectedTo);
    if (returned.searchParams.get('state') !== state) {
      throw new Error('a silent switch received a callback for a different request');
    }
    const error = returned.searchParams.get('error');
    if (error) {
      throw new Error(`silent switch to ${organization} was refused: ${error}`);
    }
    const code = returned.searchParams.get('code');
    if (!code) {
      throw new Error('silent switch did not return an authorization code');
    }

    const body = new URLSearchParams({
      grant_type: 'authorization_code',
      client_id: client.clientId,
      redirect_uri: client.redirectUri,
      code,
      code_verifier: verifier,
    });
    const response = await firstValueFrom(
      this.http.post<{ access_token: string }>(
        `${client.issuer}/protocol/openid-connect/token`,
        body.toString(),
        { headers: { 'Content-Type': 'application/x-www-form-urlencoded' } },
      ),
    );
    return response.access_token;
  }
}
