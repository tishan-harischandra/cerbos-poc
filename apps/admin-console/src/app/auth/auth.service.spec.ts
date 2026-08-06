import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from './auth.service';
import { OIDC_CONFIG } from './oidc-config';
import { REDIRECT } from './redirect';

function fakeJwt(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `${encode({ alg: 'RS256' })}.${encode(payload)}.signature`;
}

describe('AuthService', () => {
  let httpMock: HttpTestingController;
  let redirectSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    redirectSpy = vi.fn();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: OIDC_CONFIG,
          useValue: {
            issuer: 'http://localhost:8081/realms/cerbos-poc',
            clientId: 'patient-app',
            redirectUri: 'http://localhost:4200/callback',
          },
        },
        { provide: REDIRECT, useValue: redirectSpy },
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
    sessionStorage.clear();
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('is not authenticated before any token is set', () => {
    const auth = TestBed.inject(AuthService);
    expect(auth.isAuthenticated()).toBe(false);
    expect(auth.accessToken()).toBeNull();
  });

  it('redirects to the authorization endpoint with a PKCE challenge and stores the verifier and state', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();

    expect(redirectSpy).toHaveBeenCalledTimes(1);
    const url = new URL(redirectSpy.mock.calls[0][0] as string);
    expect(url.origin + url.pathname).toEqual(
      'http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/auth',
    );
    expect(url.searchParams.get('response_type')).toEqual('code');
    expect(url.searchParams.get('client_id')).toEqual('patient-app');
    expect(url.searchParams.get('code_challenge_method')).toEqual('S256');
    expect(url.searchParams.get('code_challenge')).toBeTruthy();
    expect(url.searchParams.get('state')).toEqual(sessionStorage.getItem('admin-console:pkce-state'));
    expect(sessionStorage.getItem('admin-console:pkce-verifier')).toBeTruthy();
  });

  it('exchanges the code for a token and becomes authenticated on a matching state', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();
    const state = sessionStorage.getItem('admin-console:pkce-state')!;

    const token = fakeJwt({
      sub: 'admin-1',
      preferred_username: 'admin',
      tenant_id: 'tenant-a',
      hospital_id: 'hospital-1',
      resource_access: { 'patient-app': { roles: ['administrator'] } },
    });

    const promise = auth.handleCallback('auth-code-1', state);
    httpMock
      .expectOne('http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token')
      .flush({ access_token: token });

    expect(await promise).toBe(true);
    expect(auth.isAuthenticated()).toBe(true);
    expect(auth.accessToken()).toEqual(token);
    expect(auth.claims()?.tenantId).toEqual('tenant-a');
    expect(auth.claims()?.hospitalId).toEqual('hospital-1');
  });

  it('refuses a callback whose state does not match the one login stored', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();

    const result = await auth.handleCallback('auth-code-1', 'a-forged-state');

    expect(result).toBe(false);
    expect(auth.isAuthenticated()).toBe(false);
    httpMock.expectNone('http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token');
  });

  it('deletes the stored verifier and state after one callback attempt, matching or not', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();

    await auth.handleCallback('auth-code-1', 'a-forged-state');

    expect(sessionStorage.getItem('admin-console:pkce-verifier')).toBeNull();
    expect(sessionStorage.getItem('admin-console:pkce-state')).toBeNull();
  });

  it('returns false when the token exchange itself fails', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();
    const state = sessionStorage.getItem('admin-console:pkce-state')!;

    const promise = auth.handleCallback('auth-code-1', state);
    httpMock
      .expectOne('http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token')
      .flush({ error: 'invalid_grant' }, { status: 400, statusText: 'Bad Request' });

    expect(await promise).toBe(false);
    expect(auth.isAuthenticated()).toBe(false);
  });

  it('clears the token and redirects to the end-session endpoint on logout', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();
    const state = sessionStorage.getItem('admin-console:pkce-state')!;
    const promise = auth.handleCallback(
      'auth-code-1',
      state,
    );
    httpMock
      .expectOne('http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/token')
      .flush({ access_token: fakeJwt({ sub: 'admin-1' }) });
    await promise;

    auth.logout();

    expect(auth.isAuthenticated()).toBe(false);
    const url = new URL(redirectSpy.mock.calls[1][0] as string);
    expect(url.origin + url.pathname).toEqual(
      'http://localhost:8081/realms/cerbos-poc/protocol/openid-connect/logout',
    );
  });
});
