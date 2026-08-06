import {
  HttpContextToken,
  HttpErrorResponse,
  HttpInterceptorFn,
} from '@angular/common/http';
import { inject } from '@angular/core';
import { catchError, throwError } from 'rxjs';

import { CAPABILITY_INSTANCE_KEY } from './capability-instance-key';
import { CapabilitySnapshotService } from './capability-snapshot.service';

const ALREADY_RETRIED = new HttpContextToken<boolean>(() => false);

/**
 * capabilityRetryInterceptor implements §12.6's stale-snapshot recovery:
 * "if an API returns 403 because a snapshot became stale, invalidate the
 * affected context snapshot and refresh once before showing the final
 * denial." It invalidates the CapabilitySnapshotService cache entry named
 * by the request's CAPABILITY_INSTANCE_KEY (or every cached instance
 * snapshot if the request never set one) and retries the original request
 * exactly once; a second 403 propagates as the final denial.
 */
export const capabilityRetryInterceptor: HttpInterceptorFn = (req, next) => {
  const snapshots = inject(CapabilitySnapshotService);

  return next(req).pipe(
    catchError((error: unknown) => {
      if (
        !(error instanceof HttpErrorResponse) ||
        error.status !== 403 ||
        req.context.get(ALREADY_RETRIED)
      ) {
        return throwError(() => error);
      }

      const instanceKey = req.context.get(CAPABILITY_INSTANCE_KEY);
      if (instanceKey) {
        snapshots.invalidateInstance(instanceKey);
      } else {
        snapshots.invalidateAllInstances();
      }

      const retried = req.clone({
        context: req.context.set(ALREADY_RETRIED, true),
      });
      return next(retried);
    }),
  );
};
