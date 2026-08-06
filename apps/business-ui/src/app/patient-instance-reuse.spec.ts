import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { CapabilityStore, UiCapabilitySnapshot } from '@cerbos-poc/capability';

import { App } from './app';
import { appRoutes } from './app.routes';

function snapshot(): UiCapabilitySnapshot {
  return {
    authorizationRevision: 1,
    capabilityCatalogRevision: 'ui-capabilities-v1',
    module: 'clinical',
    contextFingerprint: 'sha256:abc',
    capabilities: {
      'patient.route.details': { allowed: true },
      'patient.route.edit': { allowed: true },
    },
  };
}

describe('navigating between a loaded patient resource and its child routes', () => {
  it('fetches the instance snapshot once and reuses it for the edit child route with no further request', async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter(appRoutes),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    });
    const httpMock = TestBed.inject(HttpTestingController);
    const store = TestBed.inject(CapabilityStore);
    const router = TestBed.inject(Router);
    // A mounted <router-outlet> is required for the Router to actually
    // instantiate a routed component (and so run its ngOnInit); without
    // one, navigateByUrl only ever updates the URL.
    const appFixture = TestBed.createComponent(App);
    appFixture.detectChanges();

    // capabilityGuard must already grant 'patient.route.details' before the
    // module snapshot has loaded, otherwise the route never matches and the
    // instance fetch this test is asserting on would never happen.
    store.replace({
      authorizationRevision: 0,
      capabilityCatalogRevision: 'ui-capabilities-v0',
      module: 'clinical',
      contextFingerprint: 'sha256:pre',
      capabilities: { 'patient.route.details': { allowed: true } },
    });

    await router.navigateByUrl('/patients/patient-456');
    appFixture.detectChanges();
    httpMock
      .expectOne('/api/ads/internal/capabilities/evaluate')
      .flush(snapshot());
    await appFixture.whenStable();

    expect(store.can('patient.route.edit')).toBe(true);

    await router.navigateByUrl('/patients/patient-456/edit');
    appFixture.detectChanges();

    httpMock.expectNone('/api/ads/internal/capabilities/evaluate');
    httpMock.verify();
  });
});
