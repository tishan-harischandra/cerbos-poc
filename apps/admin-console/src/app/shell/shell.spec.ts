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
            }),
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
