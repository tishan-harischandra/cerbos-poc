import { Location } from '@angular/common';
import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { CAPABILITY_API_BASE_URL, UiCapabilitySnapshot } from '@cerbos-poc/capability';

import { appRoutes } from './app.routes';
import { AuthService } from './auth/auth.service';

const EVALUATE_URL = '/api/ads/internal/capabilities/evaluate';

function grantingSnapshot(): UiCapabilitySnapshot {
  return {
    authorizationRevision: 186,
    capabilityCatalogRevision: 'ui-capabilities-v1',
    module: 'clinical',
    contextFingerprint: 'sha256:abc',
    capabilities: { 'patients.route.list': { allowed: true } },
  };
}

/** Lets the router's pipeline advance to the point of issuing the fetch. */
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// The defect: capabilityGuard used to sit second in the same canMatch
// array as the guard that loads the snapshot, on the assumption that the
// router awaited each in turn. It does not - runCanMatchGuards invokes
// every guard on a route eagerly and concurrently, then takes the first
// non-true result by position. So the capability check read an empty
// store and redirected to /forbidden while the snapshot was still in
// flight, and a fully-permitted clinician was shown "forbidden" on login.
//
// Unlike the other routing specs, this one uses the real ClinicalSession
// and an empty store: the snapshot arriving mid-navigation is the whole
// point, so stubbing it out would make the test pass no matter how the
// guards were ordered.
describe('navigating to a capability-guarded route while the snapshot is still loading', () => {
  it('waits for the module snapshot and matches the route rather than redirecting to /forbidden', async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter(appRoutes),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: CAPABILITY_API_BASE_URL, useValue: '/api/ads' },
        {
          provide: AuthService,
          useValue: {
            isAuthenticated: () => true,
            accessToken: () => 'a-token',
            login: () => Promise.resolve(),
            takeReturnTo: () => '/',
            logout: () => undefined,
          },
        },
      ],
    });
    const httpMock = TestBed.inject(HttpTestingController);
    const router = TestBed.inject(Router);

    const navigation = router.navigateByUrl('/patients');
    await settle();

    httpMock.expectOne(EVALUATE_URL).flush(grantingSnapshot());
    await navigation;

    expect(TestBed.inject(Location).path()).toBe('/patients');
    httpMock.verify();
  });
});
