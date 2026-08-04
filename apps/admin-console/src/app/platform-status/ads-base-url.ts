import { InjectionToken } from '@angular/core';

/**
 * Base URL of the Assignment Data Service as seen from the browser. In docker
 * compose the admin console is served behind a reverse proxy that forwards
 * this prefix to the ADS container.
 */
export const ADS_BASE_URL = new InjectionToken<string>('ADS_BASE_URL', {
  providedIn: 'root',
  factory: () => '/api/ads',
});
