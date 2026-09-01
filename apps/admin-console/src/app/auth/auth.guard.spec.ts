import { TestBed } from '@angular/core/testing';

import { authGuard } from './auth.guard';
import { AuthService } from './auth.service';

describe('authGuard', () => {
  it('allows navigation once AuthService reports an authenticated session', () => {
    const auth = { isAuthenticated: () => true, login: vi.fn() };
    TestBed.configureTestingModule({ providers: [{ provide: AuthService, useValue: auth }] });

    const result = TestBed.runInInjectionContext(() =>
      authGuard({} as never, {} as never),
    );

    expect(result).toBe(true);
    expect(auth.login).not.toHaveBeenCalled();
  });

  it('starts login and refuses navigation when there is no session yet', () => {
    const auth = { isAuthenticated: () => false, login: vi.fn().mockResolvedValue(undefined) };
    TestBed.configureTestingModule({ providers: [{ provide: AuthService, useValue: auth }] });

    const result = TestBed.runInInjectionContext(() =>
      authGuard({} as never, {} as never),
    );

    expect(result).toBe(false);
    expect(auth.login).toHaveBeenCalledTimes(1);
  });

  it('carries the requested URL into login, so a deep link survives (issue #82)', () => {
    const auth = { isAuthenticated: () => false, login: vi.fn().mockResolvedValue(undefined) };
    TestBed.configureTestingModule({ providers: [{ provide: AuthService, useValue: auth }] });

    TestBed.runInInjectionContext(() =>
      authGuard({} as never, { url: '/role-matrix' } as never),
    );

    expect(auth.login).toHaveBeenCalledWith('/role-matrix');
  });
});
