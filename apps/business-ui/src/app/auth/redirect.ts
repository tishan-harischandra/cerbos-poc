import { InjectionToken } from '@angular/core';

/**
 * The one seam between AuthService and a real browser navigation.
 *
 * jsdom's `window.location.assign` cannot be reassigned or spied on
 * directly (it is a non-configurable platform property), so this
 * InjectionToken is what a test overrides instead of touching `window`.
 */
export const REDIRECT = new InjectionToken<(url: string) => void>('REDIRECT', {
  providedIn: 'root',
  factory: () => (url: string) => window.location.assign(url),
});
