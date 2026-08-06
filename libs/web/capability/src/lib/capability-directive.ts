import {
  Directive,
  effect,
  inject,
  Input,
  signal,
  TemplateRef,
  ViewContainerRef,
} from '@angular/core';

import { CapabilityDecision } from './capability-decision';
import { CapabilityStore } from './capability-store';

/**
 * *libCapability shows or hides its host template by capability key
 * (§12.5). It reads the CapabilityStore's signal reactively, so a
 * snapshot refresh (a tenant switch, a revision change, a 403 retry)
 * re-evaluates every rendered `*libCapability` without the page needing
 * to reload.
 *
 * It never fetches protected data on the caller's behalf: the data behind
 * a `*libCapability`-gated component must already have been requested
 * through an authorized code path, or not requested at all (§12.6 -
 * never fetch protected data and then hide it with CSS).
 */
@Directive({
  selector: '[libCapability]',
  standalone: true,
})
export class CapabilityDirective {
  private readonly store = inject(CapabilityStore);
  private readonly templateRef = inject(TemplateRef<unknown>);
  private readonly viewContainer = inject(ViewContainerRef);

  private readonly key = signal<string | undefined>(undefined);
  // capabilityDecisions is a local, caller-supplied decision map that, when
  // set, is used instead of the CapabilityStore (§12.7: row-level menus
  // rendered from a single batched list response, never a per-row store
  // lookup or a per-row request).
  private readonly localDecisions = signal<
    Record<string, CapabilityDecision> | undefined
  >(undefined);
  private rendered = false;

  constructor() {
    effect(() => {
      const key = this.key();
      const local = this.localDecisions();
      const allowed =
        key !== undefined &&
        (local ? local[key]?.allowed === true : this.store.can(key));
      if (allowed && !this.rendered) {
        this.viewContainer.createEmbeddedView(this.templateRef);
        this.rendered = true;
      } else if (!allowed && this.rendered) {
        this.viewContainer.clear();
        this.rendered = false;
      }
    });
  }

  @Input()
  set libCapability(key: string) {
    this.key.set(key);
  }

  @Input()
  set libCapabilityDecisions(decisions: Record<string, CapabilityDecision> | undefined) {
    this.localDecisions.set(decisions);
  }
}
