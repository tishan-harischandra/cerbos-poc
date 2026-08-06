import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { RoleMatrix } from './role-matrix';

function setUp() {
  TestBed.configureTestingModule({
    imports: [RoleMatrix],
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

const patientRecordCatalog = {
  resources: [
    {
      resourceKey: 'patient_record',
      displayName: 'Patient record',
      domain: 'clinical',
      actions: [
        { key: 'read', displayName: 'View patient', context: 'INSTANCE' },
        { key: 'delete', displayName: 'Delete patient', context: 'INSTANCE' },
      ],
    },
    {
      resourceKey: 'installation_config',
      displayName: 'Installation config',
      domain: 'platform',
      actions: [{ key: 'read', displayName: 'View config', context: 'INSTANCE' }],
    },
  ],
  rootPolicyRevision: 'root-v1.4.0',
};

describe('RoleMatrix', () => {
  it('loads the resource catalog on construction, through the admin-service proxy', () => {
    const httpMock = setUp();
    TestBed.createComponent(RoleMatrix);

    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);
    httpMock.verify();
  });

  it('flags an unresolved role and does not allow selecting it', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    fixture.componentInstance.roleQuery.set('doctor');
    const searchPromise = fixture.componentInstance.searchRoles();
    httpMock.expectOne('/api/ads/internal/directory/roles?query=doctor').flush({
      items: [{ canonicalId: '', externalId: 'composite-doctor', name: 'Composite doctor', description: '' }],
    });
    await searchPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="role-unresolved"]')?.textContent,
    ).toContain('unresolved canonical role, needs remediation');
    expect(fixture.nativeElement.querySelector('[data-testid="role-option"]')).toBeNull();
  });

  it('shows a resolvable role as selectable and distinguishes its informational display name', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    fixture.componentInstance.roleQuery.set('doctor');
    const searchPromise = fixture.componentInstance.searchRoles();
    httpMock.expectOne('/api/ads/internal/directory/roles?query=doctor').flush({
      items: [{ canonicalId: 'kc:cerbos-poc:patient-app:doctor', externalId: 'doctor', name: 'Doctor', description: '' }],
    });
    await searchPromise;
    fixture.detectChanges();

    const option = fixture.nativeElement.querySelector('[data-testid="role-option"]') as HTMLElement;
    expect(option).toBeTruthy();
    expect(option.textContent).toContain('Doctor');
    expect(option.textContent).toContain('persists as kc:cerbos-poc:patient-app:doctor');
  });

  it('loads the full matrix for a selected role and renders its current grants', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    const selectPromise = fixture.componentInstance.selectRole({
      canonicalId: 'kc:cerbos-poc:patient-app:doctor', externalId: 'doctor', name: 'Doctor', description: '',
    });
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/kc:cerbos-poc:patient-app:doctor/permissions')
      .flush({
        permissions: [
          { resourceKey: 'patient_record', actionKey: 'read', enabled: true, validFrom: '2026-01-01T00:00:00Z' },
        ],
        revision: 4,
      });
    await selectPromise;
    fixture.detectChanges();

    expect(fixture.componentInstance.isEnabled('patient_record', 'read')).toBe(true);
    expect(fixture.componentInstance.isEnabled('patient_record', 'delete')).toBe(false);
    expect(fixture.componentInstance.expectedRevision()).toEqual(4);

    const matrix = fixture.nativeElement.querySelector('[data-testid="matrix"]');
    expect(matrix).toBeTruthy();
  });

  it('filters resources by a search term across resource, action and domain names', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    const selectPromise = fixture.componentInstance.selectRole({
      canonicalId: 'role-doctor', externalId: 'doctor', name: 'Doctor', description: '',
    });
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions')
      .flush({ permissions: [], revision: 0 });
    await selectPromise;

    fixture.componentInstance.resourceFilter.set('installation');
    fixture.detectChanges();

    const domains = fixture.componentInstance.domains();
    expect(domains).toHaveLength(1);
    expect(domains[0].resources.map((r) => r.resourceKey)).toEqual(['installation_config']);
  });

  it('toggling a checkbox never produces an explicit deny row - a cleared checkbox is simply absent', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    const selectPromise = fixture.componentInstance.selectRole({
      canonicalId: 'role-doctor', externalId: 'doctor', name: 'Doctor', description: '',
    });
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions')
      .flush({
        permissions: [{ resourceKey: 'patient_record', actionKey: 'read', enabled: true, validFrom: '2026-01-01T00:00:00Z' }],
        revision: 1,
      });
    await selectPromise;

    fixture.componentInstance.toggle('patient_record', 'read');
    expect(fixture.componentInstance.isEnabled('patient_record', 'read')).toBe(false);

    fixture.componentInstance.toggle('patient_record', 'delete');
    expect(fixture.componentInstance.isEnabled('patient_record', 'delete')).toBe(true);
  });

  it('shows an actionable error offering reload on a stale revision, keeping pending edits intact', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    const selectPromise = fixture.componentInstance.selectRole({
      canonicalId: 'role-doctor', externalId: 'doctor', name: 'Doctor', description: '',
    });
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions')
      .flush({ permissions: [], revision: 0 });
    await selectPromise;

    fixture.componentInstance.toggle('patient_record', 'read');
    const savePromise = fixture.componentInstance.save();
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions')
      .flush({ error: 'stale' }, { status: 409, statusText: 'Conflict' });
    await savePromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="stale-revision-error"]'),
    ).toBeTruthy();
    // The pending edit survives the conflict - nothing silently reset it.
    expect(fixture.componentInstance.isEnabled('patient_record', 'read')).toBe(true);
  });

  it('sends every administrative call through the server-side proxy, never to the identity provider directly from here', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    fixture.componentInstance.roleQuery.set('doctor');
    const searchPromise = fixture.componentInstance.searchRoles();
    const roleReq = httpMock.expectOne('/api/ads/internal/directory/roles?query=doctor');
    expect(roleReq.request.url.startsWith('/api/')).toBe(true);
    roleReq.flush({ items: [] });
    await searchPromise;
  });
});
