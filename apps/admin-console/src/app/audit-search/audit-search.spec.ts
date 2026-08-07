import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { AuditSearch } from './audit-search';

function setUp() {
  TestBed.configureTestingModule({
    imports: [AuditSearch],
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

describe('AuditSearch', () => {
  it('states its tenant-scoped authority up front', () => {
    setUp();
    const fixture = TestBed.createComponent(AuditSearch);
    fixture.detectChanges();

    const note = fixture.nativeElement.querySelector('[data-testid="scope-note"]') as HTMLElement;
    expect(note.textContent).toContain('tenant-a');
  });

  it('searches with the tenant and every filter the administrator entered', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(AuditSearch);
    fixture.detectChanges();

    fixture.componentInstance.actor.set('admin-2');
    fixture.componentInstance.role.set('role-doctor');
    const searchPromise = fixture.componentInstance.search();

    const req = httpMock.expectOne(
      (r) =>
        r.url === '/api/admin/authz/audit' &&
        r.params.get('tenant') === 'tenant-a' &&
        r.params.get('actor') === 'admin-2' &&
        r.params.get('role') === 'role-doctor',
    );
    req.flush({
      events: [
        {
          eventId: 'audit-1', actorId: 'admin-2', operation: 'ROLE_MATRIX_SAVE',
          targetType: 'role_permission', before: '{}', after: '{}',
          tenantId: 'tenant-a', roleExternalId: 'role-doctor',
          resourceActionKeys: 'patient_record:read', createdAt: '2026-06-01T12:00:00Z',
        },
      ],
      totalCount: 1,
    });
    await searchPromise;

    fixture.detectChanges();
    const rows = fixture.nativeElement.querySelectorAll('[data-testid="audit-row"]');
    expect(rows.length).toBe(1);
    const total = fixture.nativeElement.querySelector('[data-testid="total-count"]') as HTMLElement;
    expect(total.textContent).toContain('1 total');
  });

  it('sends a full RFC3339 timestamp for a date typed as datetime-local, not the bare local string', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(AuditSearch);
    fixture.detectChanges();

    // What a <input type="datetime-local"> actually produces: no
    // timezone offset at all.
    fixture.componentInstance.from.set('2026-06-01T12:00');
    const searchPromise = fixture.componentInstance.search();

    const req = httpMock.expectOne(
      (r) => r.url === '/api/admin/authz/audit' && r.params.has('from'),
    );
    const from = req.request.params.get('from')!;
    expect(() => new Date(from)).not.toThrow();
    expect(Number.isNaN(Date.parse(from))).toBe(false);
    expect(from).toMatch(/Z$|[+-]\d\d:\d\d$/);

    req.flush({ events: [], totalCount: 0 });
    await searchPromise;
  });

  it('reports a search failure without leaving stale results on screen', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(AuditSearch);
    fixture.detectChanges();

    const searchPromise = fixture.componentInstance.search();
    httpMock.expectOne((r) => r.url === '/api/admin/authz/audit').flush(
      { error: 'boom' },
      { status: 500, statusText: 'Internal Server Error' },
    );
    await searchPromise;

    fixture.detectChanges();
    const error = fixture.nativeElement.querySelector('[data-testid="search-error"]') as HTMLElement;
    expect(error.textContent).toContain('failed');
    expect(fixture.nativeElement.querySelectorAll('[data-testid="audit-row"]').length).toBe(0);
  });

  it('has no control anywhere in the template that edits or deletes an audit event', () => {
    setUp();
    const fixture = TestBed.createComponent(AuditSearch);
    fixture.detectChanges();

    const html = (fixture.nativeElement as HTMLElement).innerHTML.toLowerCase();
    expect(html).not.toContain('delete');
    expect(html).not.toContain('data-testid="edit-');
  });
});
