import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { CAPABILITY_API_BASE_URL } from './capability-api-base-url';
import { UiCapabilitySnapshot } from './capability-decision';
import { CapabilityStore } from './capability-store';

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

  /**
   * loadModule fetches a module- and collection-level snapshot (§12.6):
   * at login, and again on every tenant or hospital switch.
   */
  async loadModule(
    module: string,
    capabilityKeys: string[],
  ): Promise<UiCapabilitySnapshot> {
    const snapshot = await this.fetch(module, capabilityKeys);
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
