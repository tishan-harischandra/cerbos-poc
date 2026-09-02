import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { Organizations } from './organizations';

function setUp() {
  TestBed.configureTestingModule({
    imports: [Organizations],
    providers: [provideHttpClient(), provideHttpClientTesting()],
  });
  return TestBed.inject(HttpTestingController);
}

describe('Organizations', () => {
  it("lists a tenant's organizations (issue #85)", async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Organizations);
    httpMock.expectOne('/api/ads/internal/directory/organizations?offset=0&limit=20').flush({
      items: [
        { externalId: 'org-north', alias: 'north-hospital', name: 'North Hospital' },
        { externalId: 'org-south', alias: 'south-hospital', name: 'South Hospital' },
      ],
      offset: 0, limit: 20, hasMore: false,
    });
    await Promise.resolve();
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="organization-option-north-hospital"]'),
    ).toBeTruthy();
    expect(
      fixture.nativeElement.querySelector('[data-testid="organization-option-south-hospital"]'),
    ).toBeTruthy();
  });

  it("shows an organization's members on selection", async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Organizations);
    httpMock.expectOne('/api/ads/internal/directory/organizations?offset=0&limit=20').flush({
      items: [{ externalId: 'org-north', alias: 'north-hospital', name: 'North Hospital' }],
      offset: 0, limit: 20, hasMore: false,
    });
    await Promise.resolve();
    fixture.detectChanges();

    (fixture.nativeElement.querySelector(
      '[data-testid="organization-option-north-hospital"]',
    ) as HTMLButtonElement).click();
    httpMock
      .expectOne('/api/ads/internal/directory/organizations/org-north/members?offset=0&limit=20')
      .flush({
        items: [{ externalId: 'user-doctor', username: 'doctor', displayName: 'Dana Doctor' }],
        offset: 0, limit: 20, hasMore: false,
      });
    await Promise.resolve();
    fixture.detectChanges();

    const members = fixture.nativeElement.querySelector(
      '[data-testid="member-user-doctor"]',
    ) as HTMLElement;
    expect(members.textContent).toContain('Dana Doctor');
  });

  it('shows an error when the organization list itself fails to load', async () => {
    const httpMock = setUp();
    const fixture = TestBed.createComponent(Organizations);
    httpMock.expectOne('/api/ads/internal/directory/organizations?offset=0&limit=20').flush(
      { error: 'unavailable' },
      { status: 503, statusText: 'Service Unavailable' },
    );
    await Promise.resolve();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="organizations-error"]')).toBeTruthy();
  });
});
