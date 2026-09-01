import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { vi } from 'vitest';

import { HospitalSwitcher } from './hospital-switcher';
import { OidcClient } from './oidc-client';
import { SILENT_FRAME } from './silent-frame';

const CLIENT: OidcClient = {
  issuer: 'http://localhost:8081/realms/tenant-a',
  clientId: 'patient-app',
  redirectUri: 'http://localhost:4200/callback',
};

/** Extracts the state Keycloak was asked to echo back, from the URL the fake silent frame received. */
function stateFrom(authorizeUrl: string): string {
  return new URL(authorizeUrl).searchParams.get('state') ?? '';
}

/**
 * Lets the PKCE code-challenge derivation (a real crypto.subtle.digest
 * await) and the fake silent frame's own promise settle before a test
 * asserts on the HTTP call switchTo makes only afterwards.
 */
function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

describe('HospitalSwitcher', () => {
  let httpMock: HttpTestingController;
  let service: HospitalSwitcher;
  let silentFrame: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    silentFrame = vi.fn();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: SILENT_FRAME, useValue: silentFrame },
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
    service = TestBed.inject(HospitalSwitcher);
  });

  afterEach(() => httpMock.verify());

  it('requests a silent authorization scoped to the target organization and exchanges the code', async () => {
    silentFrame.mockImplementation(async (authorizeUrl: string) => {
      expect(authorizeUrl).toContain(`${CLIENT.issuer}/protocol/openid-connect/auth?`);
      expect(authorizeUrl).toContain('prompt=none');
      expect(authorizeUrl).toContain('scope=openid+organization%3Asouth-hospital');
      return `${CLIENT.redirectUri}?code=a-fresh-code&state=${stateFrom(authorizeUrl)}`;
    });

    const resultPromise = service.switchTo(CLIENT, 'south-hospital');
    await flushMicrotasks();

    const req = httpMock.expectOne(`${CLIENT.issuer}/protocol/openid-connect/token`);
    expect(req.request.method).toBe('POST');
    const body = new URLSearchParams(req.request.body as string);
    expect(body.get('grant_type')).toEqual('authorization_code');
    expect(body.get('code')).toEqual('a-fresh-code');
    expect(body.get('code_verifier')).toBeTruthy();
    req.flush({ access_token: 'south-hospital-token' });

    expect(await resultPromise).toEqual('south-hospital-token');
  });

  it('fails and issues no token exchange when the silent request is refused', async () => {
    silentFrame.mockImplementation(
      async (authorizeUrl: string) => `${CLIENT.redirectUri}?error=interaction_required&state=${stateFrom(authorizeUrl)}`,
    );

    await expect(service.switchTo(CLIENT, 'a-hospital-not-a-member-of')).rejects.toThrow(/refused/);
    httpMock.expectNone(`${CLIENT.issuer}/protocol/openid-connect/token`);
  });

  it('fails on a state mismatch rather than trusting a stale or forged callback', async () => {
    silentFrame.mockResolvedValue(`${CLIENT.redirectUri}?code=some-code&state=not-the-request-state`);

    await expect(service.switchTo(CLIENT, 'south-hospital')).rejects.toThrow(/different request/);
    httpMock.expectNone(`${CLIENT.issuer}/protocol/openid-connect/token`);
  });

  it('fails when the silent frame never reaches the redirect URI at all', async () => {
    silentFrame.mockRejectedValue(new Error('silent switch timed out waiting for a response'));

    await expect(service.switchTo(CLIENT, 'south-hospital')).rejects.toThrow(/timed out/);
    httpMock.expectNone(`${CLIENT.issuer}/protocol/openid-connect/token`);
  });
});
