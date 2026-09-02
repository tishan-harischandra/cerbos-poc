import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { Simulator } from './simulator';

function setUp(hospitalId: string | undefined = 'hospital-1') {
  TestBed.configureTestingModule({
    imports: [Simulator],
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

describe('Simulator', () => {
  it('states its own tenant/hospital scope', () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    const note = fixture.nativeElement.querySelector('[data-testid="scope-note"]') as HTMLElement;
    expect(note.textContent).toContain('tenant-a');
    expect(note.textContent).toContain('hospital-1');
    httpMock.verify();
  });

  it('refuses to offer the screen at all to a tenant-wide session (ADR-012)', () => {
    const httpMock = setUp('');
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    const notice = fixture.nativeElement.querySelector(
      '[data-testid="no-hospital-scope"]',
    ) as HTMLElement;
    expect(notice).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="scope-note"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="access-simulator"]')).toBeNull();
    httpMock.verify();
  });

  it('reports the decision source for an access simulation', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    fixture.componentInstance.principalId.set('user-doctor');
    fixture.componentInstance.resourceKind.set('patient_record');
    fixture.componentInstance.resourceId.set('patient-456');
    fixture.componentInstance.action.set('read');

    const runPromise = fixture.componentInstance.runAccessSimulation();
    const req = httpMock.expectOne('/api/admin/authz/simulate');
    expect(req.request.body.tenantId).toEqual('tenant-a');
    expect(req.request.body.hospitalId).toEqual('hospital-1');
    expect(req.request.body.principalId).toEqual('user-doctor');
    req.flush({ cerbosCallId: 'call-1', permissionRevision: 4, allowed: true, source: 'ROLE' });
    await runPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="access-source"]')?.textContent,
    ).toContain('ROLE');
    expect(
      fixture.nativeElement.querySelector('[data-testid="access-allowed"]')?.textContent,
    ).toContain('true');
  });

  // §19's acceptance criterion naming this exact scenario.
  it('reports MANDATORY_RULE for a LOCKED sample attribute', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    fixture.componentInstance.principalId.set('user-doctor');
    fixture.componentInstance.resourceKind.set('patient_record');
    fixture.componentInstance.resourceId.set('patient-456');
    fixture.componentInstance.resourceAttributes.set('{"status": "LOCKED"}');
    fixture.componentInstance.action.set('update');

    const runPromise = fixture.componentInstance.runAccessSimulation();
    const req = httpMock.expectOne('/api/admin/authz/simulate');
    expect(req.request.body.resource.attributes).toEqual({ status: 'LOCKED' });
    req.flush({ cerbosCallId: 'call-2', permissionRevision: 4, allowed: false, source: 'MANDATORY_RULE' });
    await runPromise;
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="access-source"]')?.textContent,
    ).toContain('MANDATORY_RULE');
  });

  it('returns the full requirement tree for a capability simulation, with each leaf decision', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    fixture.componentInstance.capabilityModule.set('clinical');
    fixture.componentInstance.capabilityKeys.set('patient.route.edit');
    fixture.componentInstance.capabilityPrincipalId.set('user-doctor');

    const runPromise = fixture.componentInstance.runCapabilitySimulation();
    const req = httpMock.expectOne('/api/admin/authz/simulate-capabilities');
    expect(req.request.body.tenantId).toEqual('tenant-a');
    expect(req.request.body.capabilityKeys).toEqual(['patient.route.edit']);
    req.flush({
      authorizationRevision: 1,
      rootPolicyRevision: 'root-v1.4.0',
      capabilityCatalogRevision: '1',
      capabilities: { 'patient.route.edit': { allowed: false, reason: 'REQUIRED_PERMISSION_DENIED' } },
      requirementTree: [
        { resource: 'patient_record', action: 'read', target: 'sample:patient', allowed: true, reason: 'ROLE' },
        { resource: 'patient_record', action: 'update', target: 'sample:patient', allowed: false, reason: 'USER_REVOKE' },
      ],
    });
    await runPromise;
    fixture.detectChanges();

    const tree = fixture.nativeElement.querySelector('[data-testid="requirement-tree"]') as HTMLElement;
    expect(tree.textContent).toContain('read');
    expect(tree.textContent).toContain('update');
    expect(tree.textContent).toContain('USER_REVOKE');

    expect(
      fixture.nativeElement.querySelector('[data-testid="capability-outcomes"]')?.textContent,
    ).toContain('denied');
  });

  it('rejects malformed JSON in the resource attributes without calling the server', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Simulator);
    fixture.detectChanges();

    fixture.componentInstance.resourceAttributes.set('not json');
    await fixture.componentInstance.runAccessSimulation();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="access-error"]')).toBeTruthy();
    httpMock.expectNone('/api/admin/authz/simulate');
  });
});
