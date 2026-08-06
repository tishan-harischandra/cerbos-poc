import { provideLocationMocks } from '@angular/common/testing';
import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Router, convertToParamMap } from '@angular/router';

import { AuthService } from './auth.service';
import { Callback } from './callback';

describe('Callback', () => {
  function setUp(queryParams: Record<string, string>, handleCallback = vi.fn()) {
    TestBed.configureTestingModule({
      imports: [Callback],
      providers: [
        provideLocationMocks(),
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { queryParamMap: convertToParamMap(queryParams) },
          },
        },
        { provide: AuthService, useValue: { handleCallback, login: vi.fn() } },
      ],
    });
    return { handleCallback };
  }

  it('exchanges the code and state for a token, then navigates to the shell', async () => {
    const handleCallback = vi.fn().mockResolvedValue(true);
    setUp({ code: 'auth-code-1', state: 'state-1' }, handleCallback);

    const fixture = TestBed.createComponent(Callback);
    fixture.detectChanges();
    await fixture.whenStable();

    expect(handleCallback).toHaveBeenCalledWith('auth-code-1', 'state-1');
    expect(TestBed.inject(Router).url).toEqual('/');
  });

  it('shows a retry prompt when the exchange fails', async () => {
    setUp({ code: 'auth-code-1', state: 'state-1' }, vi.fn().mockResolvedValue(false));

    const fixture = TestBed.createComponent(Callback);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="callback-failed"]'),
    ).toBeTruthy();
  });

  it('shows a retry prompt when Keycloak redirected back with no code', async () => {
    setUp({});

    const fixture = TestBed.createComponent(Callback);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(
      fixture.nativeElement.querySelector('[data-testid="callback-failed"]'),
    ).toBeTruthy();
  });
});
