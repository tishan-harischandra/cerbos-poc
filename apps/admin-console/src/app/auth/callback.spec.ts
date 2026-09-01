import { provideLocationMocks } from '@angular/common/testing';
import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Router, convertToParamMap, provideRouter } from '@angular/router';

import { AuthService } from './auth.service';
import { Callback } from './callback';

describe('Callback', () => {
  function setUp(
    queryParams: Record<string, string>,
    handleCallback = vi.fn(),
    consumeReturnTo = vi.fn().mockReturnValue(null),
  ) {
    TestBed.configureTestingModule({
      imports: [Callback],
      providers: [
        provideLocationMocks(),
        // A wildcard route so navigateByUrl can actually recognise a deep
        // link like /role-matrix; this suite is not exercising what those
        // routes render, only where Callback sends the browser.
        provideRouter([{ path: '**', children: [] }]),
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { queryParamMap: convertToParamMap(queryParams) },
          },
        },
        { provide: AuthService, useValue: { handleCallback, login: vi.fn(), consumeReturnTo } },
      ],
    });
    return { handleCallback, consumeReturnTo };
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

  it('navigates to the deep link a guarded route redirected away from, when there is one', async () => {
    const handleCallback = vi.fn().mockResolvedValue(true);
    setUp(
      { code: 'auth-code-1', state: 'state-1' },
      handleCallback,
      vi.fn().mockReturnValue('/role-matrix'),
    );

    const fixture = TestBed.createComponent(Callback);
    fixture.detectChanges();
    await fixture.whenStable();
    await fixture.whenStable();

    expect(TestBed.inject(Router).url).toEqual('/role-matrix');
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
