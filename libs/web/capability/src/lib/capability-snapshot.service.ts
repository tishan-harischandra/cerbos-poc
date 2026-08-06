import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { CAPABILITY_API_BASE_URL } from './capability-api-base-url';
import { UiCapabilitySnapshot } from './capability-decision';
import { CapabilityStore } from './capability-store';

/**
 * RevisionPair is the pair of revisions that must independently stay
 * current for a cached snapshot to remain valid (§12.6): the
 * authorization revision (role/user assignment changes) and the
 * capability-catalog revision (UI-capability definition changes).
 */
export interface RevisionPair {
  authorizationRevision?: number;
  capabilityCatalogRevision?: string;
}

/**
 * CapabilitySnapshotService fetches §12.4 capability snapshots from the
 * ADS capability evaluator (issue #11's `POST
 * /internal/capabilities/evaluate`) and replaces them into the
 * CapabilityStore. It never evaluates a capability expression itself
 * (§12.5) - the ADS already did that.
 */
@Injectable({ providedIn: 'root' })
export class CapabilitySnapshotService {
  private readonly http = inject(HttpClient);
  private readonly store = inject(CapabilityStore);
  private readonly baseUrl = inject(CAPABILITY_API_BASE_URL);

  // Keyed by the caller's own instance key (e.g. "patient:patient-456"),
  // one entry per loaded page resource (§12.6). There is no eviction
  // policy here: entries live only as long as the invalidation methods
  // below, a tenant/hospital switch (clear()), or a browser tab do.
  private readonly instanceSnapshots = new Map<string, UiCapabilitySnapshot>();

  // The most recent revision pair any loaded snapshot carried. noteRevisions
  // compares an API response's revisions against this to decide whether the
  // cache has gone stale (§12.6).
  private lastKnownRevisions: RevisionPair | undefined;

  /**
   * loadModule fetches a module- and collection-level snapshot (§12.6):
   * at login, and again on every tenant or hospital switch. Every cached
   * instance snapshot is dropped first - it was scoped to whichever
   * tenant and hospital were active when it was fetched, and calling
   * loadModule is itself the signal that this may have just changed.
   */
  async loadModule(
    module: string,
    capabilityKeys: string[],
  ): Promise<UiCapabilitySnapshot> {
    this.invalidateAllInstances();
    const snapshot = await this.fetch(module, capabilityKeys);
    this.rememberRevisions(snapshot);
    this.store.replace(snapshot);
    return snapshot;
  }

  /**
   * loadInstance fetches an instance-context snapshot once per
   * instanceKey and reuses it across every subsequent call with the same
   * key - child routes and tabs of an already-loaded page resource issue
   * no further network call (§12.6).
   */
  async loadInstance(
    module: string,
    capabilityKeys: string[],
    instanceKey: string,
    context: Record<string, string>,
  ): Promise<UiCapabilitySnapshot> {
    const cached = this.instanceSnapshots.get(instanceKey);
    if (cached) {
      this.store.replace(cached);
      return cached;
    }

    const snapshot = await this.fetch(module, capabilityKeys, context);
    this.rememberRevisions(snapshot);
    this.instanceSnapshots.set(instanceKey, snapshot);
    this.store.replace(snapshot);
    return snapshot;
  }

  /**
   * invalidateInstance drops a cached instance snapshot, forcing the next
   * loadInstance call for that key to fetch a fresh one - used when a
   * business endpoint reports the snapshot went stale (§12.6).
   */
  invalidateInstance(instanceKey: string): void {
    this.instanceSnapshots.delete(instanceKey);
  }

  /**
   * invalidateAllInstances drops every cached instance snapshot - used
   * when a business endpoint reports staleness without naming which
   * resource it was scoped to, and on a tenant or hospital switch.
   */
  invalidateAllInstances(): void {
    this.instanceSnapshots.clear();
  }

  /**
   * noteRevisions is how a business API response reports its revisions
   * back to the shared library (§12.6: "return authorization and
   * capability-catalog revisions from backend APIs and refresh when
   * either changes"). A revision that differs from the last one any
   * loaded snapshot carried invalidates every cached instance snapshot,
   * so the next load for that resource fetches a fresh one.
   */
  noteRevisions(revisions: RevisionPair): void {
    if (this.lastKnownRevisions && this.revisionsMatch(this.lastKnownRevisions, revisions)) {
      return;
    }
    this.lastKnownRevisions = revisions;
    this.invalidateAllInstances();
  }

  private rememberRevisions(snapshot: UiCapabilitySnapshot): void {
    this.lastKnownRevisions = {
      authorizationRevision: snapshot.authorizationRevision,
      capabilityCatalogRevision: snapshot.capabilityCatalogRevision,
    };
  }

  private revisionsMatch(a: RevisionPair, b: RevisionPair): boolean {
    return (
      (a.authorizationRevision === undefined ||
        b.authorizationRevision === undefined ||
        a.authorizationRevision === b.authorizationRevision) &&
      (a.capabilityCatalogRevision === undefined ||
        b.capabilityCatalogRevision === undefined ||
        a.capabilityCatalogRevision === b.capabilityCatalogRevision)
    );
  }

  private fetch(
    module: string,
    capabilityKeys: string[],
    context?: Record<string, string>,
  ): Promise<UiCapabilitySnapshot> {
    const body: Record<string, unknown> = { module, capabilityKeys };
    if (context) body['context'] = context;
    return firstValueFrom(
      this.http.post<UiCapabilitySnapshot>(
        `${this.baseUrl}/internal/capabilities/evaluate`,
        body,
      ),
    );
  }
}
