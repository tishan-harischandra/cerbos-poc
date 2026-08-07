import { Component, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { IdPDiagnosticsApi, IdPDiagnosticsResult } from './idp-diagnostics-api';

/**
 * The Admin Console's IdP diagnostics module (§9.1, §17.1, issue #22):
 * the selected identity provider, its role and token mapping
 * configuration, and a live connectivity check - degraded search here
 * never implies degraded runtime authorization, which never depends on
 * the identity provider being reachable.
 */
@Component({
  standalone: true,
  imports: [],
  selector: 'app-idp-diagnostics',
  templateUrl: './idp-diagnostics.html',
  styleUrl: './idp-diagnostics.css',
})
export class IdPDiagnostics {
  private readonly api = inject(IdPDiagnosticsApi);

  readonly diagnostics = signal<IdPDiagnosticsResult | null>(null);
  readonly error = signal<string | null>(null);

  constructor() {
    void this.load();
  }

  private async load(): Promise<void> {
    try {
      const result = await firstValueFrom(this.api.getDiagnostics());
      this.diagnostics.set(result);
    } catch {
      this.error.set('The identity provider diagnostics could not be loaded.');
    }
  }
}
