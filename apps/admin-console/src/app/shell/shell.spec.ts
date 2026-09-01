import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';

import { AuthService } from '../auth/auth.service';
import { Shell } from './shell';

describe('Shell', () => {
  it('shows the logged-in identity and logs out on demand', () => {
    const logout = vi.fn();
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            claims: () => ({
              subject: 'admin-1',
              username: 'admin',
              tenantId: 'tenant-a',
              hospitalId: 'hospital-1',
              roles: [],
              expiresAt: 0,
              isAdministrator: false,
              otherHospitals: [],
            }),
            keycloakConsoleUrl: () => 'http://localhost:8081/admin/tenant-a/console/',
            logout,
          },
        },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    const identity = fixture.nativeElement.querySelector(
      '[data-testid="shell-identity"]',
    ) as HTMLElement;
    expect(identity.textContent).toContain('admin');
    expect(identity.textContent).toContain('tenant-a / hospital-1');

    (fixture.nativeElement.querySelector('[data-testid="logout"]') as HTMLButtonElement).click();
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it('shows a link to Keycloak\'s administration console for an administrator', () => {
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            claims: () => ({
              subject: 'admin-1',
              username: 'admin',
              tenantId: 'tenant-a',
              hospitalId: '',
              roles: [],
              expiresAt: 0,
              isAdministrator: true,
              otherHospitals: [],
            }),
            keycloakConsoleUrl: () => 'http://localhost:8081/admin/tenant-a/console/',
            logout: vi.fn(),
          },
        },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    const link = fixture.nativeElement.querySelector(
      '[data-testid="keycloak-console-link"]',
    ) as HTMLAnchorElement;
    expect(link).toBeTruthy();
    expect(link.href).toEqual('http://localhost:8081/admin/tenant-a/console/');
  });

  it('shows no Keycloak console link for a user who is not an administrator', () => {
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            claims: () => ({
              subject: 'doctor-1',
              username: 'doctor',
              tenantId: 'tenant-a',
              hospitalId: 'north-hospital',
              roles: [],
              expiresAt: 0,
              isAdministrator: false,
              otherHospitals: [],
            }),
            keycloakConsoleUrl: () => 'http://localhost:8081/admin/tenant-a/console/',
            logout: vi.fn(),
          },
        },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    expect(
      fixture.nativeElement.querySelector('[data-testid="keycloak-console-link"]'),
    ).toBeNull();
  });

  it('offers a switcher listing every other hospital and switches on selection (issue #84)', async () => {
    const switchHospital = vi.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            claims: () => ({
              subject: 'doctor-1',
              username: 'doctor',
              tenantId: 'tenant-a',
              hospitalId: 'north-hospital',
              roles: [],
              expiresAt: 0,
              isAdministrator: false,
              otherHospitals: ['south-hospital'],
            }),
            keycloakConsoleUrl: () => 'http://localhost:8081/admin/tenant-a/console/',
            logout: vi.fn(),
            switchHospital,
          },
        },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    const select = fixture.nativeElement.querySelector(
      '[data-testid="hospital-switcher"]',
    ) as HTMLSelectElement;
    expect(select).toBeTruthy();
    expect(
      fixture.nativeElement.querySelector('[data-testid="hospital-option-south-hospital"]'),
    ).toBeTruthy();

    select.value = 'south-hospital';
    select.dispatchEvent(new Event('change'));
    await fixture.whenStable();

    expect(switchHospital).toHaveBeenCalledWith('south-hospital');
  });

  it('shows no switcher when there are no other hospitals to switch to', () => {
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: AuthService,
          useValue: {
            claims: () => ({
              subject: 'doctor-1',
              username: 'doctor',
              tenantId: 'tenant-a',
              hospitalId: 'north-hospital',
              roles: [],
              expiresAt: 0,
              isAdministrator: false,
              otherHospitals: [],
            }),
            keycloakConsoleUrl: () => 'http://localhost:8081/admin/tenant-a/console/',
            logout: vi.fn(),
          },
        },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    expect(fixture.nativeElement.querySelector('[data-testid="hospital-switcher"]')).toBeNull();
  });

  it('shows no identity block before login completes', () => {
    TestBed.configureTestingModule({
      imports: [Shell],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: { claims: () => null, logout: vi.fn() } },
      ],
    });

    const fixture = TestBed.createComponent(Shell);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController).expectOne('/api/ads/healthz').flush({ status: 'ok' });

    expect(
      fixture.nativeElement.querySelector('[data-testid="shell-identity"]'),
    ).toBeNull();
  });
});
