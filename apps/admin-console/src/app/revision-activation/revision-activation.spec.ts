import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { RevisionActivation } from './revision-activation';

function setUp() {
  TestBed.configureTestingModule({
    imports: [RevisionActivation],
    providers: [
      provideHttpClient(),
      provideHttpClientTesting(),
      {
        provide: AuthService,
        useValue: { claims: () => ({ tenantId: 'tenant-a', hospitalId: 'hospital-1' }) },
      },
    ],
  });
  return TestBed.inject(HttpTestingController);
}

describe('RevisionActivation', () => {
  it('loads the current root policy revision and convergence on construction', () => {
    const httpMock = setUp();
    TestBed.createComponent(RevisionActivation);

    httpMock.expectOne('/api/admin/authz/policy-releases').flush({
      current: { revision: 'root-v1.4.0', commit: 'bbb', sha256: 'deadbeef' },
      history: [],
    });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/convergence').flush({
      tenant: 'tenant-a', cachedRevision: 4, actualRevision: 4, converged: true, replicasBehindTarget: 0,
    });
    httpMock.verify();
  });

  it('shows the current revision and marks it converged', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RevisionActivation);
    httpMock.expectOne('/api/admin/authz/policy-releases').flush({
      current: { revision: 'root-v1.4.0', commit: 'bbb', sha256: 'deadbeef' },
      history: [],
    });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/convergence').flush({
      tenant: 'tenant-a', cachedRevision: 4, actualRevision: 4, converged: true, replicasBehindTarget: 0,
    });
    await Promise.resolve();
    fixture.detectChanges();

    const revision = fixture.nativeElement.querySelector(
      '[data-testid="current-root-revision"]',
    ) as HTMLElement;
    expect(revision.textContent).toContain('root-v1.4.0');

    const convergence = fixture.nativeElement.querySelector(
      '[data-testid="convergence-status"]',
    ) as HTMLElement;
    expect(convergence.textContent).toContain('converged');
  });

  it('reports replicas behind target when the cache has not caught up', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RevisionActivation);
    httpMock.expectOne('/api/admin/authz/policy-releases').flush({ current: null, history: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/convergence').flush({
      tenant: 'tenant-a', cachedRevision: 3, actualRevision: 5, converged: false, replicasBehindTarget: 1,
    });
    await Promise.resolve();
    fixture.detectChanges();

    const convergence = fixture.nativeElement.querySelector(
      '[data-testid="convergence-status"]',
    ) as HTMLElement;
    expect(convergence.textContent).toContain('1 replica(s) behind target');
  });

  it('distinguishes a failed release attempt from a successful one in history', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RevisionActivation);
    httpMock.expectOne('/api/admin/authz/policy-releases').flush({
      current: { revision: 'root-v1.4.0', commit: 'bbb', sha256: 'deadbeef' },
      history: [
        { revision: 'root-v1.4.0', commit: 'bbb', activated: true, recordedAt: '2026-06-01T12:00:00Z' },
        {
          revision: 'root-v1.5.0', commit: 'ccc', activated: false,
          error: 'replica cerbos-b failed to reload', recordedAt: '2026-06-02T12:00:00Z',
        },
      ],
    });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/convergence').flush({
      tenant: 'tenant-a', cachedRevision: 4, actualRevision: 4, converged: true, replicasBehindTarget: 0,
    });
    await Promise.resolve();
    fixture.detectChanges();

    const rows = fixture.nativeElement.querySelectorAll('[data-testid="release-history-row"]');
    expect(rows.length).toBe(2);
    expect(rows[0].getAttribute('data-outcome')).toBe('activated');
    expect(rows[1].getAttribute('data-outcome')).toBe('failed');
    expect(rows[1].textContent).toContain('replica cerbos-b failed to reload');
  });
});
