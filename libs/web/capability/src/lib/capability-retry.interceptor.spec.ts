import {
  HttpClient,
  HttpContext,
  provideHttpClient,
  withInterceptors,
} from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { firstValueFrom } from 'rxjs';
import { vi } from 'vitest';

import { capabilityRetryInterceptor } from './capability-retry.interceptor';
import { CapabilitySnapshotService } from './capability-snapshot.service';
import { CAPABILITY_API_BASE_URL } from './capability-api-base-url';
import { CAPABILITY_INSTANCE_KEY } from './capability-instance-key';

describe('capabilityRetryInterceptor', () => {
  let httpMock: HttpTestingController;
  let http: HttpClient;
  let service: CapabilitySnapshotService;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([capabilityRetryInterceptor])),
        provideHttpClientTesting(),
        { provide: CAPABILITY_API_BASE_URL, useValue: '/api/ads' },
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
    http = TestBed.inject(HttpClient);
    service = TestBed.inject(CapabilitySnapshotService);
  });

  afterEach(() => httpMock.verify());

  it('invalidates the affected instance snapshot and retries exactly once on a 403', async () => {
    const invalidateInstance = vi.spyOn(service, 'invalidateInstance');

    const result = firstValueFrom(
      http.get('/api/business/patients/patient-456', {
        context: new HttpContext().set(
          CAPABILITY_INSTANCE_KEY,
          'patient:patient-456',
        ),
      }),
    );

    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });
    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush({ ok: true });

    await expect(result).resolves.toEqual({ ok: true });
    expect(invalidateInstance).toHaveBeenCalledWith('patient:patient-456');
  });

  it('shows the final denial after exactly one retry still fails', async () => {
    const result = firstValueFrom(
      http.get('/api/business/patients/patient-456', {
        context: new HttpContext().set(
          CAPABILITY_INSTANCE_KEY,
          'patient:patient-456',
        ),
      }),
    );

    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });
    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });

    await expect(result).rejects.toMatchObject({ status: 403 });
    httpMock.expectNone('/api/business/patients/patient-456');
  });
});
