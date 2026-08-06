import { InjectionToken } from '@angular/core';

/**
 * Base URL of the Authorization Administration Service as seen from the
 * browser, mirroring ADS_BASE_URL (platform-status/ads-base-url.ts): the
 * console never talks to admin-service directly, nginx proxies this
 * prefix to it so the backend stays on the internal compose network
 * (§16.1).
 */
export const ADMIN_BASE_URL = new InjectionToken<string>('ADMIN_BASE_URL', {
  providedIn: 'root',
  factory: () => '/api/admin',
});
