import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';

import { App } from './app';
import { appRoutes } from './app.routes';
import { AuthService } from './auth/auth.service';

describe('App', () => {
  it('redirects an authenticated administrator from / to the role matrix, inside the shell', async () => {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter(appRoutes),
        {
          provide: AuthService,
          useValue: {
            isAuthenticated: () => true,
            claims: () => ({
              tenantId: 'tenant-a',
              hospitalId: 'hospital-1',
              username: 'admin',
              otherHospitals: [],
            }),
            login: vi.fn(),
            logout: vi.fn(),
          },
        },
      ],
    });

    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl('/');
    const httpMock = TestBed.inject(HttpTestingController);
    httpMock.expectOne('/api/admin/authz/resources').flush({ resources: [], rootPolicyRevision: 'root-v1.4.0' });

    expect(TestBed.inject(Router).url).toEqual('/role-matrix');
    expect(
      harness.routeNativeElement?.querySelector('[data-testid="nav-role-matrix"]'),
    ).toBeTruthy();
  });

  it('starts login rather than reaching any guarded route when there is no session', async () => {
    const login = vi.fn();
    TestBed.configureTestingModule({
      imports: [App],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter(appRoutes),
        {
          provide: AuthService,
          useValue: { isAuthenticated: () => false, claims: () => null, login, logout: vi.fn() },
        },
      ],
    });

    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl('/');

    expect(login).toHaveBeenCalledTimes(1);
  });
});
