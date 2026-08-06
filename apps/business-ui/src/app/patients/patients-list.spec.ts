import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { PatientsList } from './patients-list';

describe('PatientsList', () => {
  it('renders a row menu from the list response with no HTTP client provided', async () => {
    // No provideHttpClient/provideHttpClientTesting here at all: this
    // proves the row menu never issues a per-row (or any) request (§12.7).
    TestBed.configureTestingModule({
      imports: [PatientsList],
      providers: [provideRouter([])],
    });

    const fixture = TestBed.createComponent(PatientsList);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    const rows = fixture.nativeElement.querySelectorAll(
      '[data-testid="patient-row"]',
    );
    expect(rows.length).toBe(2);

    const editLinks = fixture.nativeElement.querySelectorAll(
      '[data-testid="patient-row-edit"]',
    );
    expect(editLinks.length).toBe(1);
    expect(rows[0].textContent).toContain('Jordan Rivers');
    expect(rows[0].textContent).toContain('Edit');
    expect(rows[1].textContent).toContain('Sam Okafor');
    expect(rows[1].textContent).not.toContain('Edit');
  });
});
