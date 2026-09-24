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
    btoa(JSON.stringify(value))
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/, '');
  return `${encode({ alg: 'RS256' })}.${encode(payload)}.signature`;
}

describe('AuthService', () => {
  let httpMock: HttpTestingController;
  let redirectSpy: ReturnType<typeof vi.fn>;

  function configure(): void {
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
      .expectOne(
        'http://localhost:8081/realms/tenant-a/protocol/openid-connect/token',
      )
      .flush({ access_token: token });

    expect(await promise).toBe(true);
    expect(auth.claims()?.tenantId).toEqual('tenant-a');
    expect(auth.claims()?.hospitalId).toEqual('north-hospital');
  });

  it('starts a top-level PKCE transition for the target hospital', async () => {
    const auth = TestBed.inject(AuthService);

    await auth.switchHospital('south-hospital');

    expect(redirectSpy).toHaveBeenCalledTimes(1);
    const url = new URL(redirectSpy.mock.calls[0][0] as string);
    expect(url.searchParams.get('scope')).toBe(
      'openid organization:south-hospital',
    );
    expect(url.searchParams.get('prompt')).toBe('none');
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
    expect(sessionStorage.getItem('business-ui:oidc-transition')).toBe(
      'hospital-switch',
    );
    expect(sessionStorage.getItem('business-ui:switch-target')).toBe(
      'south-hospital',
    );
  });

  it('commits a switch token only when it names the pending target hospital', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.switchHospital('south-hospital');
    const state = sessionStorage.getItem('business-ui:pkce-state')!;
    const token = fakeJwt({
      sub: 'doctor-1',
      organization: ['south-hospital'],
    });

    const callback = auth.handleCallback('switch-code', state);
    httpMock
      .expectOne(
        'http://localhost:8081/realms/tenant-a/protocol/openid-connect/token',
      )
      .flush({ access_token: token });

    expect(await callback).toBe(true);
    expect(auth.accessToken()).toBe(token);
    expect(auth.claims()?.hospitalId).toBe('south-hospital');
  });

  it('rejects a switch token naming a different hospital', async () => {
    const auth = TestBed.inject(AuthService);
    await auth.switchHospital('south-hospital');
    const state = sessionStorage.getItem('business-ui:pkce-state')!;
    const token = fakeJwt({
      sub: 'doctor-1',
      organization: ['north-hospital'],
    });

    const callback = auth.handleCallback('switch-code', state);
    httpMock
      .expectOne(
        'http://localhost:8081/realms/tenant-a/protocol/openid-connect/token',
      )
      .flush({ access_token: token });

    expect(await callback).toBe(false);
    expect(auth.accessToken()).toBeNull();
    expect(sessionStorage.getItem('business-ui:switch-target')).toBeNull();
  });
});
