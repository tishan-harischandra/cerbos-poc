import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from './admin-base-url';

/** One composite UI capability that depends on a resource-action
 * permission (§6.1, §9.1's capability impact module). */
export interface CapabilityRef {
  key: string;
  module: string;
  context: string;
}

export interface CapabilityImpactResult {
  capabilities: CapabilityRef[];
}

/**
 * `GET /admin/authz/resources/{resource}/actions/{action}/capabilities`
 * (issue #18): every composite UI capability whose expression references
 * a resource-action, shared by the resource catalog module's own impact
 * view and the role matrix's pre-save impact preview - one read, not two
 * copies of the same index.
 */
@Injectable({ providedIn: 'root' })
export class CapabilityImpactApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  getCapabilityImpact(resourceKey: string, actionKey: string): Observable<CapabilityImpactResult> {
    return this.http.get<CapabilityImpactResult>(
      `${this.adminBaseUrl}/authz/resources/${resourceKey}/actions/${actionKey}/capabilities`,
    );
  }
}
