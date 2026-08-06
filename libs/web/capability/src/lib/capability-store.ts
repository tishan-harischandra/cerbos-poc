import { Injectable, signal } from '@angular/core';

import { CapabilityDecision, UiCapabilitySnapshot } from './capability-decision';

/**
 * CapabilityStore holds the browser's already-evaluated capability
 * decisions (§12.5). It never parses expressions or applies permission
 * precedence itself - that happened once, server-side, in the ADS
 * capability evaluator (§12.3); the store is a thin, synchronous read
 * surface over the last snapshot received.
 */
@Injectable({ providedIn: 'root' })
export class CapabilityStore {
  private readonly state = signal<Record<string, CapabilityDecision>>({});

  can(key: string): boolean {
    return this.state()[key]?.allowed === true;
  }

  replace(snapshot: UiCapabilitySnapshot): void {
    this.state.set(snapshot.capabilities);
  }
}
