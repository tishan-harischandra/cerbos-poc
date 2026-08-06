import {
  provideHttpClient,
  withInterceptors,
} from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { capabilityRetryInterceptor } from '@cerbos-poc/capability';

import { PatientEdit } from './patient-edit';

function activatedRouteWithParentId(id: string) {
  return {
    parent: { snapshot: { paramMap: convertToParamMap({ id }) } },
  } as unknown as ActivatedRoute;
}

describe('PatientEdit', () => {
  let httpMock: HttpTestingController;

  function setUp() {
    TestBed.configureTestingModule({
      imports: [PatientEdit],
      providers: [
        provideHttpClient(withInterceptors([capabilityRetryInterceptor])),
        provideHttpClientTesting(),
        {
          provide: ActivatedRoute,
          useValue: activatedRouteWithParentId('patient-456'),
        },
      ],
    });
    httpMock = TestBed.inject(HttpTestingController);
    return TestBed.createComponent(PatientEdit);
  }

  afterEach(() => httpMock.verify());

  it('retries exactly once on a 403 and then shows saved', () => {
    const fixture = setUp();
    fixture.detectChanges();

    fixture.nativeElement
      .querySelector('[data-testid="save"]')
      .dispatchEvent(new Event('click'));

    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });
    httpMock.expectOne('/api/business/patients/patient-456').flush({});
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="status"]')
        ?.textContent,
    ).toContain('Saved');
  });

  it('shows the final denial after a retry still fails', () => {
    const fixture = setUp();
    fixture.detectChanges();

    fixture.nativeElement
      .querySelector('[data-testid="save"]')
      .dispatchEvent(new Event('click'));

    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });
    httpMock
      .expectOne('/api/business/patients/patient-456')
      .flush(null, { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="status"]')
        ?.textContent,
    ).toContain('no longer have permission');
    httpMock.expectNone('/api/business/patients/patient-456');
  });
});
