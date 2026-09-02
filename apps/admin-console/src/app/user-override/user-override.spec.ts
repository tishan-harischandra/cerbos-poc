import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { UserOverride } from './user-override';

const catalog = {
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
  ],
  rootPolicyRevision: 'root-v1.4.0',
};

function setUp(hospitalId: string | undefined = 'hospital-1') {
  TestBed.configureTestingModule({
    imports: [UserOverride],
    providers: [
      provideHttpClient(),
      provideHttpClientTesting(),
      {
        provide: AuthService,
        useValue: { claims: () => ({ tenantId: 'tenant-a', hospitalId }) },
      },
    ],
  });
  return TestBed.inject(HttpTestingController);
}

describe('UserOverride', () => {
  it('states its hospital-scoped authority up front', () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    fixture.detectChanges();

    const note = fixture.nativeElement.querySelector('[data-testid="scope-note"]') as HTMLElement;
    expect(note.textContent).toContain('tenant-a');
    expect(note.textContent).toContain('hospital-1');
  });

  it('refuses to offer the screen at all to a tenant-wide session (ADR-012)', () => {
    const httpMock = setUp('');
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    fixture.detectChanges();

    const notice = fixture.nativeElement.querySelector(
      '[data-testid="no-hospital-scope"]',
    ) as HTMLElement;
    expect(notice).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="scope-note"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="user-search-input"]')).toBeNull();
  });

  it("shows a user's hospital memberships before granting or revoking a permission (issue #85)", async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: 'Dana Doctor', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({
      items: [
        { externalId: 'org-north', alias: 'north-hospital', name: 'North Hospital' },
        { externalId: 'org-south', alias: 'south-hospital', name: 'South Hospital' },
      ],
    });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="user-organization-north-hospital"]'),
    ).toBeTruthy();
    expect(
      fixture.nativeElement.querySelector('[data-testid="user-organization-south-hospital"]'),
    ).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="no-user-organizations"]')).toBeNull();
  });

  it('shows no memberships when a user belongs to no hospital', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: 'Dana Doctor', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="no-user-organizations"]')).toBeTruthy();
  });

  it('distinguishes Inherit, Grant and Revoke as three separate controls', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: 'Dana Doctor', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="effect-inherit"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="effect-grant"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="effect-revoke"]')).toBeTruthy();
  });

  it('shows both the role result and the effective result from a preview before saving', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({
      items: [{ canonicalId: 'role-doctor', externalId: 'doctor', name: 'Doctor', description: '' }],
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 3 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('delete');
    fixture.componentInstance.effect.set('REVOKE');

    const previewPromise = fixture.componentInstance.runPreview();
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides/preview')
      .flush({ roleResult: true, effectiveResult: false, noPracticalEffect: false });
    await previewPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="role-result"]')?.textContent,
    ).toContain('allow');
    expect(
      fixture.nativeElement.querySelector('[data-testid="effective-result"]')?.textContent,
    ).toContain('deny');
  });

  it('shows a no-op warning when the preview reports no practical effect', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('read');

    const previewPromise = fixture.componentInstance.runPreview();
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides/preview')
      .flush({ roleResult: true, effectiveResult: true, noPracticalEffect: true });
    await previewPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="no-op-warning"]'),
    ).toBeTruthy();
  });

  it('prevents saving a GRANT or REVOKE with no reason', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('read');
    fixture.componentInstance.effect.set('GRANT');

    expect(fixture.componentInstance.canSave()).toBe(false);

    fixture.componentInstance.reason.set('a reason');
    fixture.componentInstance.validFrom.set('2026-01-01T00:00');
    expect(fixture.componentInstance.canSave()).toBe(true);
  });

  it('allows saving INHERIT with no reason', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('read');
    fixture.componentInstance.effect.set('INHERIT');

    expect(fixture.componentInstance.canSave()).toBe(true);
  });

  it('shows the applied bounded expiry a high-risk grant defaulted to', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('delete');
    fixture.componentInstance.effect.set('GRANT');
    fixture.componentInstance.reason.set('one-off task');
    fixture.componentInstance.validFrom.set('2026-01-01T00:00');

    const savePromise = fixture.componentInstance.save();
    httpMock
      .expectOne(
        (req) => req.method === 'PUT' && req.url === '/api/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides',
      )
      .flush({
        revision: 1, roleResult: false, effectiveResult: true, noPracticalEffect: false,
        appliedValidUntil: '2026-01-31T00:00:00Z',
      });
    await savePromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="applied-expiry-note"]')?.textContent,
    ).toContain('2026-01-31T00:00:00Z');
  });

  it('shows an actionable stale-revision error without discarding the pending edit', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    fixture.componentInstance.selectedResourceKey.set('patient_record');
    fixture.componentInstance.selectedActionKey.set('read');
    fixture.componentInstance.effect.set('GRANT');
    fixture.componentInstance.reason.set('cover');
    fixture.componentInstance.validFrom.set('2026-01-01T00:00');

    const savePromise = fixture.componentInstance.save();
    httpMock
      .expectOne('/api/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides')
      .flush({ error: 'stale' }, { status: 409, statusText: 'Conflict' });
    await savePromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="stale-revision-error"]'),
    ).toBeTruthy();
    expect(fixture.componentInstance.reason()).toEqual('cover');
  });

  it('distinguishes an expired override from an active one', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const selectPromise = fixture.componentInstance.selectUser({
      externalId: 'user-1', username: 'doctor', displayName: '', email: '', enabled: true,
    });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/roles').flush({ items: [] });
    httpMock.expectOne('/api/ads/internal/directory/users/user-1/organizations').flush({ items: [] });
    httpMock.expectOne('/api/admin/authz/tenants/tenant-a/permission-revision').flush({ revision: 0 });
    await selectPromise;

    const resourcePromise = fixture.componentInstance.selectResource('patient_record');
    httpMock
      .expectOne(
        (req) => req.url === '/api/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides',
      )
      .flush({
        overrides: [
          { actionKey: 'read', effect: 'GRANT', enabled: true, validFrom: '2020-01-01T00:00:00Z' },
          {
            actionKey: 'delete', effect: 'GRANT', enabled: true,
            validFrom: '2020-01-01T00:00:00Z', validUntil: '2021-01-01T00:00:00Z',
          },
        ],
      });
    await resourcePromise;
    fixture.detectChanges();

    const rows = fixture.componentInstance.existingOverrides();
    expect(fixture.componentInstance.isActive(rows[0])).toBe(true);
    expect(fixture.componentInstance.isActive(rows[1])).toBe(false);

    expect(
      fixture.nativeElement.querySelector('[data-testid="override-row-read"] [data-testid="active-badge"]'),
    ).toBeTruthy();
    expect(
      fixture.nativeElement.querySelector('[data-testid="override-row-delete"] [data-testid="expired-badge"]'),
    ).toBeTruthy();
  });

  it('paginates user search results', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(UserOverride);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);

    const searchPromise = fixture.componentInstance.searchUsers();
    httpMock
      .expectOne('/api/ads/internal/directory/users?offset=0&limit=20')
      .flush({ items: [{ externalId: 'user-1', username: 'a', displayName: '', email: '', enabled: true }], offset: 0, limit: 20, hasMore: true });
    await searchPromise;
    fixture.detectChanges();

    expect(fixture.componentInstance.userHasMore()).toBe(true);

    const nextPromise = fixture.componentInstance.nextPage();
    httpMock
      .expectOne('/api/ads/internal/directory/users?offset=20&limit=20')
      .flush({ items: [], offset: 20, limit: 20, hasMore: false });
    await nextPromise;

    expect(fixture.componentInstance.userOffset()).toEqual(20);
    expect(fixture.componentInstance.userHasMore()).toBe(false);
  });
});
