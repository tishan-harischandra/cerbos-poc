import { Injectable, inject } from '@angular/core';
import { CapabilitySnapshotService } from '@cerbos-poc/capability';

/**
 * The capability keys the clinical module's routes and components are
 * guarded by. §12.6 asks for one small module-level snapshot covering
 * them all, rather than a call per navigation.
 */
export const CLINICAL_MODULE_CAPABILITIES = [
  'patients.route.list',
  'patient.route.details',
  'patient.route.edit',
  'patient.component.clinical-summary',
];

/**
 * Holds the once-per-session module snapshot fetch.
 *
 * §12.6 puts this "at login, before any route resolves - not once per
 * navigation". It used to run from an APP_INITIALIZER, which is strictly
 * earlier than login can possibly be: the fetch went out anonymously and
 * came back 401, and the application never recovered. Owning the pending
 * promise here lets the guard await the same in-flight fetch for
 * concurrent navigations instead of issuing a second one.
 */
@Injectable({ providedIn: 'root' })
export class ClinicalSession {
  private readonly snapshots = inject(CapabilitySnapshotService);
  private pending: Promise<unknown> | null = null;

  ensureModuleSnapshot(): Promise<unknown> {
    this.pending ??= this.snapshots
      .loadModule('clinical', CLINICAL_MODULE_CAPABILITIES)
      .catch((error: unknown) => {
        // A failed load must not be cached as "done", or the session is
        // permanently capability-less until the tab is reloaded.
        this.pending = null;
        throw error;
      });
    return this.pending;
  }

  /** Drops the snapshot, so the next navigation fetches a fresh one. */
  reset(): void {
    this.pending = null;
  }
}
