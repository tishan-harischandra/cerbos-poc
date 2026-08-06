import { TestBed } from '@angular/core/testing';
import { provideRouter, Router, UrlTree } from '@angular/router';

import { capabilityGuard } from './capability-guard';
import { CapabilityStore } from './capability-store';
import { UiCapabilitySnapshot } from './capability-decision';

function route(capability: string) {
  return { data: { capability } } as never;
}

describe('capabilityGuard', () => {
  it('matches the route when the store already grants the required capability', () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const store = TestBed.inject(CapabilityStore);
    const snapshot: UiCapabilitySnapshot = {
      authorizationRevision: 1,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: { 'patient.route.details': { allowed: true } },
    };
    store.replace(snapshot);

    const result = TestBed.runInInjectionContext(() =>
      capabilityGuard(route('patient.route.details'), {} as never),
    );

    expect(result).toBe(true);
  });

  it('redirects to /forbidden when the store denies the required capability', () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const router = TestBed.inject(Router);

    const result = TestBed.runInInjectionContext(() =>
      capabilityGuard(route('patient.route.edit'), {} as never),
    );

    expect(result).not.toBe(true);
    expect((result as UrlTree).toString()).toBe(router.parseUrl('/forbidden').toString());
  });
});
