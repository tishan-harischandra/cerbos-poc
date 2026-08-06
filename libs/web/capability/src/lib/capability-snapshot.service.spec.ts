import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';

import { CapabilitySnapshotService } from './capability-snapshot.service';
import { CapabilityStore } from './capability-store';
import { CAPABILITY_API_BASE_URL } from './capability-api-base-url';
import { UiCapabilitySnapshot } from './capability-decision';

function snapshot(overrides: Partial<UiCapabilitySnapshot> = {}): UiCapabilitySnapshot {
  return {
    authorizationRevision: 1,
    capabilityCatalogRevision: 'ui-capabilities-v1',
    module: 'clinical',
    contextFingerprint: 'sha256:abc',
    capabilities: { 'patients.route.list': { allowed: true } },
    ...overrides,
  };
}

describe('CapabilitySnapshotService', () => {
  let httpMock: HttpTestingController;
  let service: CapabilitySnapshotService;
  let store: CapabilityStore;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: CAPABILITY_API_BASE_URL, useValue: '/api/ads' },
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
    service = TestBed.inject(CapabilitySnapshotService);
    store = TestBed.inject(CapabilityStore);
  });

  afterEach(() => httpMock.verify());

  it('fetches the module snapshot and replaces it into the store', async () => {
    const result = service.loadModule('clinical', ['patients.route.list']);

    const req = httpMock.expectOne('/api/ads/internal/capabilities/evaluate');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      module: 'clinical',
      capabilityKeys: ['patients.route.list'],
    });
    req.flush(snapshot());

    await result;
    expect(store.can('patients.route.list')).toBe(true);
  });

  it('fetches an instance snapshot once and reuses it across child routes of the same resource', async () => {
    const first = service.loadInstance('clinical', ['patient.route.details'], 'patient:patient-456', {
      patientId: 'patient-456',
    });
    const req = httpMock.expectOne('/api/ads/internal/capabilities/evaluate');
    expect(req.request.body).toEqual({
      module: 'clinical',
      capabilityKeys: ['patient.route.details'],
      context: { patientId: 'patient-456' },
    });
    req.flush(
      snapshot({ capabilities: { 'patient.route.details': { allowed: true } } }),
    );
    await first;

    store.replace(snapshot({ capabilities: {} })); // simulate navigating away and back
    await service.loadInstance(
      'clinical',
      ['patient.route.details'],
      'patient:patient-456',
      { patientId: 'patient-456' },
    );

    httpMock.expectNone('/api/ads/internal/capabilities/evaluate');
    expect(store.can('patient.route.details')).toBe(true);
  });

  it('refetches an instance snapshot after it is invalidated', async () => {
    const first = service.loadInstance(
      'clinical',
      ['patient.route.details'],
      'patient:patient-456',
      { patientId: 'patient-456' },
    );
    httpMock
      .expectOne('/api/ads/internal/capabilities/evaluate')
      .flush(snapshot());
    await first;

    service.invalidateInstance('patient:patient-456');

    const second = service.loadInstance(
      'clinical',
      ['patient.route.details'],
      'patient:patient-456',
      { patientId: 'patient-456' },
    );
    httpMock
      .expectOne('/api/ads/internal/capabilities/evaluate')
      .flush(snapshot());
    await second;
  });
});
