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
        { key: 'read', displayName: 'View patient', context: 'INSTANCE', risk: 'STANDARD' },
        { key: 'delete', displayName: 'Delete patient', context: 'INSTANCE', risk: 'ELEVATED' },
      ],
    },
    {
      resourceKey: 'installation_config',
      displayName: 'Installation config',
      domain: 'platform',
      actions: [{ key: 'read', displayName: 'View config', context: 'INSTANCE', risk: 'STANDARD' }],
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
      items: [{ canonicalId: 'kc:tenant-a:patient-app:doctor', externalId: 'doctor', name: 'Doctor', description: '' }],
    });
    await searchPromise;
    fixture.detectChanges();

    const option = fixture.nativeElement.querySelector('[data-testid="role-option"]') as HTMLElement;
    expect(option).toBeTruthy();
    expect(option.textContent).toContain('Doctor');
    expect(option.textContent).toContain('persists as kc:tenant-a:patient-app:doctor');
  });

  it('loads the full matrix for a selected role and renders its current grants', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(RoleMatrix);
    httpMock.expectOne('/api/admin/authz/resources').flush(patientRecordCatalog);

    const selectPromise = fixture.componentInstance.selectRole({
      canonicalId: 'kc:tenant-a:patient-app:doctor', externalId: 'doctor', name: 'Doctor', description: '',
    });
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/kc:tenant-a:patient-app:doctor/permissions')
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
    const savePromise = fixture.componentInstance.confirmSave();
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

  it('shows an impact preview naming affected capabilities before saving, distinguishing enable from disable', async () => {
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

    // read: enabled -> disabled. delete: disabled -> enabled.
    fixture.componentInstance.toggle('patient_record', 'read');
    fixture.componentInstance.toggle('patient_record', 'delete');

    const previewPromise = fixture.componentInstance.previewSave();
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/read/capabilities')
      .flush({ capabilities: [{ key: 'patient.route.view', module: 'clinical', context: 'INSTANCE' }] });
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/delete/capabilities')
      .flush({ capabilities: [] });
    await previewPromise;
    fixture.detectChanges();

    const preview = fixture.componentInstance.impactPreview();
    expect(preview).toHaveLength(2);
    const readRow = preview?.find((r) => r.actionKey === 'read');
    const deleteRow = preview?.find((r) => r.actionKey === 'delete');
    expect(readRow?.direction).toEqual('disable');
    expect(readRow?.capabilities.map((c) => c.key)).toEqual(['patient.route.view']);
    expect(deleteRow?.direction).toEqual('enable');
    expect(deleteRow?.capabilities).toEqual([]);

    expect(
      fixture.nativeElement.querySelector('[data-testid="impact-none"]')?.textContent,
    ).toContain('no composite capability depends on this permission');

    // Nothing was written to the server yet.
    httpMock.expectNone('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions');
  });

  it('confirming the preview performs the actual save', async () => {
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
    const previewPromise = fixture.componentInstance.previewSave();
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/read/capabilities')
      .flush({ capabilities: [] });
    await previewPromise;

    const savePromise = fixture.componentInstance.confirmSave();
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions')
      .flush({ revision: 1 });
    await savePromise;

    expect(fixture.componentInstance.expectedRevision()).toEqual(1);
    expect(fixture.componentInstance.impactPreview()).toBeNull();
  });

  it('cancelling the preview writes nothing and keeps the pending edit', async () => {
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
    const previewPromise = fixture.componentInstance.previewSave();
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/read/capabilities')
      .flush({ capabilities: [] });
    await previewPromise;

    fixture.componentInstance.cancelPreview();

    expect(fixture.componentInstance.impactPreview()).toBeNull();
    expect(fixture.componentInstance.isEnabled('patient_record', 'read')).toBe(true);
    httpMock.expectNone('/api/admin/authz/tenants/tenant-a/roles/role-doctor/permissions');
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
