import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, UrlTree, provideRouter } from '@angular/router';
import { CAPABILITY_API_BASE_URL, CapabilityStore } from '@cerbos-poc/capability';

import { AuthService } from './auth.service';
import { ClinicalSession } from './clinical-session';
import { sessionGuard } from './session.guard';

const EVALUATE_URL = '/api/ads/internal/capabilities/evaluate';

function configure(authenticated: boolean, login = vi.fn()) {
  TestBed.configureTestingModule({
    providers: [
      provideHttpClient(),
      provideHttpClientTesting(),
      provideRouter([]),
      { provide: CAPABILITY_API_BASE_URL, useValue: '/api/ads' },
      {
        provide: AuthService,
        useValue: { isAuthenticated: () => authenticated, login },
      },
    ],
  });
  return login;
}

// The guard answers synchronously when there is no session - there is
// nothing to await in that case - and returns a promise once it has to
// wait for the snapshot, so this normalises the two.
function runGuard(): Promise<boolean | UrlTree> {
  return Promise.resolve(
    TestBed.runInInjectionContext(
      () =>
        sessionGuard({ path: 'patients' }, []) as
          | boolean
          | UrlTree
          | Promise<boolean | UrlTree>,
    ),
  );
}

describe('sessionGuard', () => {
  afterEach(() => TestBed.inject(HttpTestingController).verify());

  // The defect this guard exists to prevent: the snapshot used to be
  // fetched from an APP_INITIALIZER, which runs before a login can
  // possibly have happened, so every load of the application began with a
  // 401 the router never recovered from.
  it('issues no capability call at all while the user is unauthenticated', async () => {
    const login = configure(false);

    // A redirect to /signing-in rather than a plain false: an unmatched
    // route would have the router report NG04002 for the ordinary case of
    // a user who has not logged in yet.
    const result = await runGuard();
    expect(String(result)).toBe(String(TestBed.inject(Router).parseUrl('/signing-in')));

    TestBed.inject(HttpTestingController).expectNone(EVALUATE_URL);
    expect(login).toHaveBeenCalled();
  });

  it('loads the module snapshot once the user is authenticated, and admits the route', async () => {
    configure(true);

    const allowed = runGuard();

    const request = TestBed.inject(HttpTestingController).expectOne(EVALUATE_URL);
    expect(request.request.body.module).toBe('clinical');
    request.flush({
      authorizationRevision: 1,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: { 'patients.route.list': { allowed: true } },
    });

    await expect(allowed).resolves.toBe(true);
    expect(TestBed.inject(CapabilityStore).can('patients.route.list')).toBe(true);
  });

  // The capability check reads the store synchronously in canActivate, so
  // the snapshot has to be in it by the time recognition finishes - but
  // fetching it per navigation would defeat §12.6's "at login, not once
  // per navigation".
  it('fetches the module snapshot only once across repeated navigations', async () => {
    configure(true);

    const first = runGuard();
    TestBed.inject(HttpTestingController).expectOne(EVALUATE_URL).flush({
      authorizationRevision: 1,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: {},
    });
    await first;

    await expect(runGuard()).resolves.toBe(true);
    TestBed.inject(HttpTestingController).expectNone(EVALUATE_URL);
  });
});

describe('ClinicalSession', () => {
  it('forgets the loaded snapshot on reset, so the next navigation refetches it', async () => {
    configure(true);
    const session: ClinicalSession = TestBed.inject(ClinicalSession);

    const first = session.ensureModuleSnapshot();
    TestBed.inject(HttpTestingController).expectOne(EVALUATE_URL).flush({
      authorizationRevision: 1,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: {},
    });
    await first;

    session.reset();

    const second = session.ensureModuleSnapshot();
    TestBed.inject(HttpTestingController).expectOne(EVALUATE_URL).flush({
      authorizationRevision: 2,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: {},
    });
    await second;
  });
});
