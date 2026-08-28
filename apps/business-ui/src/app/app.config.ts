import { provideHttpClient, withInterceptors } from '@angular/common/http';
import {
  ApplicationConfig,
  provideBrowserGlobalErrorListeners,
} from '@angular/core';
import { provideRouter } from '@angular/router';
import { capabilityRetryInterceptor } from '@cerbos-poc/capability';

import { appRoutes } from './app.routes';
import { authInterceptor } from './auth/auth.interceptor';

// §12.6's module-level capability snapshot is loaded at login, before any
// route resolves - not once per navigation. That load lives in
// sessionGuard, not here: an APP_INITIALIZER runs before the user can
// possibly have logged in, so fetching the snapshot from one sent an
// anonymous request the ADS rightly answered 401, and the application
// never got past it.
//
// authInterceptor is listed first so the token is attached before
// capabilityRetryInterceptor can retry a request.
export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideHttpClient(
      withInterceptors([authInterceptor, capabilityRetryInterceptor]),
    ),
    provideRouter(appRoutes),
  ],
};
