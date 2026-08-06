import {
  Directive,
  effect,
  inject,
  Input,
  signal,
  TemplateRef,
  ViewContainerRef,
} from '@angular/core';

import { CapabilityStore } from './capability-store';

/**
 * *capability shows or hides its host template by capability key (§12.5).
 * It reads the CapabilityStore's signal reactively, so a snapshot refresh
 * (a tenant switch, a revision change, a 403 retry) re-evaluates every
 * rendered `*capability` without the page needing to reload.
 *
 * It never fetches protected data on the caller's behalf: the data behind
 * a `*capability`-gated component must already have been requested through
 * an authorized code path, or not requested at all (§12.6 - never fetch
 * protected data and then hide it with CSS).
 */
@Directive({
  selector: '[capability]',
  standalone: true,
})
export class CapabilityDirective {
  private readonly store = inject(CapabilityStore);
  private readonly templateRef = inject(TemplateRef<unknown>);
  private readonly viewContainer = inject(ViewContainerRef);

  private readonly key = signal<string | undefined>(undefined);
  private rendered = false;

  constructor() {
    effect(() => {
      const key = this.key();
      const allowed = key !== undefined && this.store.can(key);
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
  set capability(key: string) {
    this.key.set(key);
  }
}
