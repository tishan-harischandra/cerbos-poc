import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';

import { PlatformStatus } from './platform-status';
import { ADS_BASE_URL } from './ads-base-url';

describe('PlatformStatus', () => {
  let httpMock: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PlatformStatus],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ADS_BASE_URL, useValue: '/api/ads' },
      ],
    }).compileComponents();

    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => httpMock.verify());

  it('shows the ADS as healthy once the health endpoint answers ok', async () => {
    const fixture = TestBed.createComponent(PlatformStatus);
    fixture.detectChanges();

    httpMock.expectOne('/api/ads/healthz').flush({ status: 'ok' });
    await fixture.whenStable();
    fixture.detectChanges();

    const badge = fixture.nativeElement.querySelector(
      '[data-testid="ads-status"]',
    ) as HTMLElement;
    expect(badge.textContent).toContain('healthy');
    expect(badge.classList).toContain('status-healthy');
  });

  it('shows the ADS as unreachable when the health endpoint cannot be reached', async () => {
    const fixture = TestBed.createComponent(PlatformStatus);
    fixture.detectChanges();

    httpMock
      .expectOne('/api/ads/healthz')
      .error(new ProgressEvent('network error'));
    await fixture.whenStable();
    fixture.detectChanges();

    const badge = fixture.nativeElement.querySelector(
      '[data-testid="ads-status"]',
    ) as HTMLElement;
    expect(badge.textContent).toContain('unreachable');
    expect(badge.classList).toContain('status-unreachable');
  });
});
