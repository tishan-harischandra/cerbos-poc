import { Component, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import {
  ConvergenceResult,
  PolicyRelease,
  PolicyReleaseHistoryEntry,
  RevisionActivationApi,
} from './revision-activation-api';

/**
 * The Admin Console's revision and activation module (§9.1, §17.1,
 * issue #22): the current root policy revision and its release history,
 * plus this tenant's cache convergence state against the ADS's own report.
 */
@Component({
  standalone: true,
  imports: [],
  selector: 'app-revision-activation',
  templateUrl: './revision-activation.html',
  styleUrl: './revision-activation.css',
})
export class RevisionActivation {
  private readonly api = inject(RevisionActivationApi);
  private readonly auth = inject(AuthService);

  readonly tenant = computed(() => this.auth.claims()?.tenantId ?? '');

  readonly currentRelease = signal<PolicyRelease | null>(null);
  readonly history = signal<PolicyReleaseHistoryEntry[]>([]);
  readonly releasesError = signal<string | null>(null);

  readonly convergence = signal<ConvergenceResult | null>(null);
  readonly convergenceError = signal<string | null>(null);

  constructor() {
    void this.loadPolicyReleases();
    void this.loadConvergence();
  }

  private async loadPolicyReleases(): Promise<void> {
    try {
      const result = await firstValueFrom(this.api.getPolicyReleases());
      this.currentRelease.set(result.current);
      this.history.set(result.history);
    } catch {
      this.releasesError.set('The release history could not be loaded.');
    }
  }

  private async loadConvergence(): Promise<void> {
    try {
      const result = await firstValueFrom(this.api.getConvergence(this.tenant()));
      this.convergence.set(result);
    } catch {
      this.convergenceError.set('The cache convergence status could not be loaded.');
    }
  }

  outcomeOf(entry: PolicyReleaseHistoryEntry): 'activated' | 'failed' {
    return entry.activated ? 'activated' : 'failed';
  }
}
