import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { Provider } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { SILENT_FRAME } from '@cerbos-poc/auth';

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

  // A test naming an extra provider - the switch tests' own fake
  // SILENT_FRAME - reconfigures the module before injecting anything, since
  // Angular refuses to override a provider once the module is instantiated.
  function configure(...extraProviders: Provider[]): void {
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: OIDC_CONFIG,
          useValue: {
            issuer: 'http://localhost:8081/realms/tenant-a',
            clientId: 'patient-app',
            redirectUri: 'http://localhost:4200/callback',
          },
        },
        { provide: REDIRECT, useValue: redirectSpy },
        ...extraProviders,
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
  }

  beforeEach(() => {
    redirectSpy = vi.fn();
    configure();
    sessionStorage.clear();
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('decodes and exposes the token claims once login completes', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.login();
    const state = sessionStorage.getItem('business-ui:pkce-state')!;

    const token = fakeJwt({
      sub: 'doctor-1',
      preferred_username: 'doctor',
      iss: 'http://localhost:8081/realms/tenant-a',
      organization: ['north-hospital'],
      resource_access: { 'patient-app': { roles: ['doctor'] } },
    });

    const promise = auth.handleCallback('auth-code-1', state);
    httpMock
      .expectOne('http://localhost:8081/realms/tenant-a/protocol/openid-connect/token')
      .flush({ access_token: token });

    expect(await promise).toBe(true);
    expect(auth.claims()?.tenantId).toEqual('tenant-a');
    expect(auth.claims()?.hospitalId).toEqual('north-hospital');
  });

  it('switches hospital silently, replacing the active token on success (issue #84)', async () => {
    const silentFrame = vi.fn().mockImplementation(async (authorizeUrl: string) => {
      const state = new URL(authorizeUrl).searchParams.get('state');
      return `http://localhost:4200/callback?code=switch-code&state=${state}`;
    });
    configure({ provide: SILENT_FRAME, useValue: silentFrame });
    const auth = TestBed.inject(AuthService);

    const token = fakeJwt({ sub: 'doctor-1', organization: ['south-hospital'] });
    const promise = auth.switchHospital('south-hospital');
    const req = await vi.waitFor(() =>
      httpMock.expectOne('http://localhost:8081/realms/tenant-a/protocol/openid-connect/token'),
    );
    req.flush({ access_token: token });

    expect(await promise).toBe(true);
    expect(auth.accessToken()).toEqual(token);
    expect(auth.claims()?.hospitalId).toEqual('south-hospital');
  });

  it('leaves the existing session intact when a silent switch is refused (issue #84)', async () => {
    const silentFrame = vi.fn().mockImplementation(async (authorizeUrl: string) => {
      const state = new URL(authorizeUrl).searchParams.get('state');
      return `http://localhost:4200/callback?error=interaction_required&state=${state}`;
    });
    configure({ provide: SILENT_FRAME, useValue: silentFrame });
    const auth = TestBed.inject(AuthService);
    await auth.login();
    const state = sessionStorage.getItem('business-ui:pkce-state')!;
    const existingToken = fakeJwt({ sub: 'doctor-1', organization: ['north-hospital'] });
    const callback = auth.handleCallback('auth-code-1', state);
    httpMock
      .expectOne('http://localhost:8081/realms/tenant-a/protocol/openid-connect/token')
      .flush({ access_token: existingToken });
    await callback;

    const result = await auth.switchHospital('a-hospital-not-a-member-of');

    expect(result).toBe(false);
    expect(auth.accessToken()).toEqual(existingToken);
  });
});
