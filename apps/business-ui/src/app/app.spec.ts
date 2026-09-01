import { provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';
import { App } from './app';
import { AuthService } from './auth/auth.service';

// Untyped against AuthService on purpose: its `claims` is a Signal, and a
// fake standing in for one only needs to be callable the same way, not an
// actual Signal instance.
function configure(authService: Record<string, unknown>): void {
  TestBed.configureTestingModule({
    imports: [App],
    providers: [provideRouter([]), { provide: AuthService, useValue: authService }],
  });
}

describe('App', () => {
  it('hosts a router outlet', async () => {
    configure({ claims: () => null });
    const fixture = TestBed.createComponent(App);
    await fixture.whenStable();
    const compiled = fixture.nativeElement as HTMLElement;
    expect(compiled.querySelector('router-outlet')).toBeTruthy();
  });

  it('shows no header before login completes', async () => {
    configure({ claims: () => null });
    const fixture = TestBed.createComponent(App);
    await fixture.whenStable();
    expect(fixture.nativeElement.querySelector('[data-testid="app-header"]')).toBeNull();
  });

  it('displays the active hospital prominently once a session exists (issue #84)', async () => {
    configure({
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
    });
    const fixture = TestBed.createComponent(App);
    await fixture.whenStable();

    expect(
      (fixture.nativeElement.querySelector('[data-testid="active-hospital"]') as HTMLElement)
        .textContent,
    ).toContain('tenant-a / north-hospital');
    expect(fixture.nativeElement.querySelector('[data-testid="hospital-switcher"]')).toBeNull();
  });

  it('offers a switcher listing every other hospital and switches on selection (issue #84)', async () => {
    const switchHospital = vi.fn().mockResolvedValue(true);
    configure({
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
      switchHospital,
    });
    const fixture = TestBed.createComponent(App);
    await fixture.whenStable();

    const select = fixture.nativeElement.querySelector(
      '[data-testid="hospital-switcher"]',
    ) as HTMLSelectElement;
    expect(select).toBeTruthy();

    select.value = 'south-hospital';
    select.dispatchEvent(new Event('change'));
    await fixture.whenStable();

    expect(switchHospital).toHaveBeenCalledWith('south-hospital');
  });
});
