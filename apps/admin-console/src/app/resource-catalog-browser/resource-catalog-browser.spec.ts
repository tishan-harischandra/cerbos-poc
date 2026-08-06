import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { ResourceCatalogBrowser } from './resource-catalog-browser';

const catalog = {
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

function setUp() {
  TestBed.configureTestingModule({
    imports: [ResourceCatalogBrowser],
    providers: [provideHttpClient(), provideHttpClientTesting()],
  });
  return TestBed.inject(HttpTestingController);
}

describe('ResourceCatalogBrowser', () => {
  it('is browsable with domain grouping, risk metadata and the current catalog revision', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(ResourceCatalogBrowser);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    await Promise.resolve();
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="catalog-revision"]')?.textContent,
    ).toContain('root-v1.4.0');
    expect(fixture.nativeElement.querySelector('[data-testid="domain-clinical"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="domain-platform"]')).toBeTruthy();
    expect(
      fixture.nativeElement.querySelector('[data-testid="risk-patient_record-delete"]')?.textContent,
    ).toContain('ELEVATED');
    expect(
      fixture.nativeElement.querySelector('[data-testid="risk-patient_record-read"]')?.textContent,
    ).toContain('STANDARD');
  });

  it('lists every capability depending on a selected resource-action', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(ResourceCatalogBrowser);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    fixture.detectChanges();

    const selectPromise = fixture.componentInstance.selectAction('patient_record', 'read');
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/read/capabilities')
      .flush({
        capabilities: [
          { key: 'patient.route.view', module: 'clinical', context: 'INSTANCE' },
          { key: 'patient.component.summary', module: 'clinical', context: 'INSTANCE' },
        ],
      });
    await selectPromise;
    fixture.detectChanges();

    const list = fixture.nativeElement.querySelector('[data-testid="impact-list"]') as HTMLElement;
    expect(list.textContent).toContain('patient.route.view');
    expect(list.textContent).toContain('patient.component.summary');
  });

  it('clearly shows a resource-action used by no capability, rather than an empty error', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(ResourceCatalogBrowser);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    fixture.detectChanges();

    const selectPromise = fixture.componentInstance.selectAction('patient_record', 'delete');
    httpMock
      .expectOne('/api/admin/authz/resources/patient_record/actions/delete/capabilities')
      .flush({ capabilities: [] });
    await selectPromise;
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="impact-empty"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="impact-error"]')).toBeNull();
  });

  it('derives the impact index from the active catalog rather than a hardcoded list', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(ResourceCatalogBrowser);
    httpMock.expectOne('/api/admin/authz/resources').flush(catalog);
    fixture.detectChanges();

    const selectPromise = fixture.componentInstance.selectAction('installation_config', 'read');
    const req = httpMock.expectOne('/api/admin/authz/resources/installation_config/actions/read/capabilities');
    expect(req.request.url.startsWith('/api/')).toBe(true);
    req.flush({ capabilities: [] });
    await selectPromise;
  });
});
