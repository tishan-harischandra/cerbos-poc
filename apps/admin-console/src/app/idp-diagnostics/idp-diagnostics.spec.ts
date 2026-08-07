import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { IdPDiagnostics } from './idp-diagnostics';

function setUp() {
  TestBed.configureTestingModule({
    imports: [IdPDiagnostics],
    providers: [provideHttpClient(), provideHttpClientTesting()],
  });
  return TestBed.inject(HttpTestingController);
}

describe('IdPDiagnostics', () => {
  it('loads and shows the selected provider with OK connectivity', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(IdPDiagnostics);
    httpMock.expectOne('/api/admin/idp/diagnostics').flush({
      provider: 'KEYCLOAK', roleSource: 'CLIENT', tenantMappingMode: 'CLAIM', connectivity: 'ok',
    });
    await Promise.resolve();
    fixture.detectChanges();

    const provider = fixture.nativeElement.querySelector(
      '[data-testid="idp-provider"]',
    ) as HTMLElement;
    expect(provider.textContent).toContain('KEYCLOAK');

    const connectivity = fixture.nativeElement.querySelector(
      '[data-testid="idp-connectivity"]',
    ) as HTMLElement;
    expect(connectivity.textContent).toContain('ok');
    expect(connectivity.getAttribute('data-status')).toBe('ok');
  });

  it('shows degraded connectivity distinctly when the IdP admin API is unreachable', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(IdPDiagnostics);
    httpMock.expectOne('/api/admin/idp/diagnostics').flush({
      provider: 'KEYCLOAK', roleSource: 'CLIENT', tenantMappingMode: 'CLAIM', connectivity: 'degraded',
    });
    await Promise.resolve();
    fixture.detectChanges();

    const connectivity = fixture.nativeElement.querySelector(
      '[data-testid="idp-connectivity"]',
    ) as HTMLElement;
    expect(connectivity.textContent).toContain('degraded');
    expect(connectivity.getAttribute('data-status')).toBe('degraded');
  });

  it('shows an error when the diagnostics request itself fails', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(IdPDiagnostics);
    httpMock.expectOne('/api/admin/idp/diagnostics').flush(
      { error: 'unavailable' },
      { status: 503, statusText: 'Service Unavailable' },
    );
    await Promise.resolve();
    fixture.detectChanges();

    const error = fixture.nativeElement.querySelector(
      '[data-testid="idp-diagnostics-error"]',
    ) as HTMLElement;
    expect(error).toBeTruthy();
  });
});
