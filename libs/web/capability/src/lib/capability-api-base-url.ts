import { InjectionToken } from '@angular/core';

/**
 * CAPABILITY_API_BASE_URL is the base URL of the ADS capability endpoint
 * (§12.3, issue #11's `POST /internal/capabilities/evaluate`). It
 * defaults to `/api/ads`, the reverse-proxy prefix apps/admin-console
 * already established for the browser-to-ADS path; an application may
 * override it for a different deployment topology.
 */
export const CAPABILITY_API_BASE_URL = new InjectionToken<string>(
  'CAPABILITY_API_BASE_URL',
  { providedIn: 'root', factory: () => '/api/ads' },
);
