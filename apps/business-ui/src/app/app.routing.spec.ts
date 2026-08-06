import { Location } from '@angular/common';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { CapabilityStore, UiCapabilitySnapshot } from '@cerbos-poc/capability';

import { appRoutes } from './app.routes';

// Every test below provides HttpClientTesting so PatientDetail's instance
// snapshot fetch (triggered as a side effect of the /patients/:id route
// matching) has somewhere safe to land; these tests never flush it, since
// they assert on routing/guard behaviour, not on the fetch itself.
const httpProviders = [provideHttpClient(), provideHttpClientTesting()];

function snapshotGranting(...keys: string[]): UiCapabilitySnapshot {
  const capabilities: UiCapabilitySnapshot['capabilities'] = {};
  for (const key of keys) capabilities[key] = { allowed: true };
  return {
    authorizationRevision: 1,
    capabilityCatalogRevision: 'ui-capabilities-v1',
    module: 'clinical',
    contextFingerprint: 'sha256:abc',
    capabilities,
  };
}

describe('appRoutes', () => {
  it('redirects to /forbidden when the store denies the route capability', async () => {
    TestBed.configureTestingModule({
      providers: [provideRouter(appRoutes), ...httpProviders],
    });
    TestBed.inject(CapabilityStore).replace(snapshotGranting());

    const router = TestBed.inject(Router);
    await router.navigateByUrl('/patients');

    expect(TestBed.inject(Location).path()).toBe('/forbidden');
  });

  it('matches the route when the store grants the route capability', async () => {
    TestBed.configureTestingModule({
      providers: [provideRouter(appRoutes), ...httpProviders],
    });
    TestBed.inject(CapabilityStore).replace(
      snapshotGranting('patients.route.list'),
    );

    const router = TestBed.inject(Router);
    await router.navigateByUrl('/patients');

    expect(TestBed.inject(Location).path()).toBe('/patients');
  });

  it('redirects to /forbidden on the edit child route while the store denies patient.route.edit', async () => {
    TestBed.configureTestingModule({
      providers: [provideRouter(appRoutes), ...httpProviders],
    });
    TestBed.inject(CapabilityStore).replace(
      snapshotGranting('patient.route.details'),
    );

    const router = TestBed.inject(Router);
    await router.navigateByUrl('/patients/patient-456/edit');

    expect(TestBed.inject(Location).path()).toBe('/forbidden');
  });
});
