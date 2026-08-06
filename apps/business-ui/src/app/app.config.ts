import { provideHttpClient, withInterceptors } from '@angular/common/http';
import {
  APP_INITIALIZER,
  ApplicationConfig,
  inject,
  provideBrowserGlobalErrorListeners,
} from '@angular/core';
import { provideRouter } from '@angular/router';
import {
  CapabilitySnapshotService,
  capabilityRetryInterceptor,
} from '@cerbos-poc/capability';

import { appRoutes } from './app.routes';

// §12.6: a small module-level capability snapshot is loaded at login,
// before any route resolves - not once per navigation. There is no
// login screen in this minimal demonstrator, so bootstrap stands in for
// it.
function loadModuleSnapshotAtLogin(): () => Promise<unknown> {
  const snapshots = inject(CapabilitySnapshotService);
  return () =>
    snapshots.loadModule('clinical', [
      'patients.route.list',
      'patient.route.details',
      'patient.route.edit',
      'patient.component.clinical-summary',
    ]);
}

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideHttpClient(withInterceptors([capabilityRetryInterceptor])),
    provideRouter(appRoutes),
    {
      provide: APP_INITIALIZER,
      useFactory: loadModuleSnapshotAtLogin,
      multi: true,
    },
  ],
};
