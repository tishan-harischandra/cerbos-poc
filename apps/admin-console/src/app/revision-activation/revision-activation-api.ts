import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';

/** One built release, as recorded on GET /admin/authz/policy-releases. */
export interface PolicyRelease {
  revision: string;
  commit: string;
  sha256: string;
}

/** One recorded release attempt, activated or not (issue #22). */
export interface PolicyReleaseHistoryEntry {
  revision: string;
  commit: string;
  activated: boolean;
  error?: string;
  recordedAt: string;
}

export interface PolicyReleasesResult {
  current: PolicyRelease | null;
  history: PolicyReleaseHistoryEntry[];
}

/** The ADS's own cache convergence report for one tenant. */
export interface ConvergenceResult {
  tenant: string;
  cachedRevision: number;
  actualRevision: number;
  converged: boolean;
  replicasBehindTarget: number;
}

@Injectable({ providedIn: 'root' })
export class RevisionActivationApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  getPolicyReleases(): Observable<PolicyReleasesResult> {
    return this.http.get<PolicyReleasesResult>(`${this.adminBaseUrl}/authz/policy-releases`);
  }

  getConvergence(tenant: string): Observable<ConvergenceResult> {
    return this.http.get<ConvergenceResult>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/convergence`,
    );
  }
}
