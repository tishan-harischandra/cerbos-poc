import { InjectionToken } from '@angular/core';

/**
 * CAPABILITY_API_BASE_URL is the base URL of the ADS capability endpoint
 * (§12.3, issue #11's `POST /internal/capabilities/evaluate`). Every
 * application that consumes this library provides its own value, the same
 * way apps/admin-console provides its local ADS_BASE_URL.
 */
export const CAPABILITY_API_BASE_URL = new InjectionToken<string>(
  'CAPABILITY_API_BASE_URL',
);
