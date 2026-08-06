import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';

/** The sample resource named in an access simulation (§9.4, issue #19). */
export interface SimulateAccessTarget {
  kind: string;
  id: string;
  attributes: Record<string, unknown>;
}

export interface SimulateAccessRequest {
  tenantId: string;
  hospitalId: string;
  principalId: string;
  idpRoles: string[];
  resource: SimulateAccessTarget;
  action: string;
}

/** Appendix A's decision source vocabulary. */
export type DecisionSource = 'MANDATORY_RULE' | 'USER_REVOKE' | 'USER_GRANT' | 'ROLE';

export interface SimulateAccessResult {
  cerbosCallId: string;
  permissionRevision: number;
  allowed: boolean;
  source: DecisionSource;
}

export interface SimulateCapabilitiesRequest {
  module: string;
  capabilityKeys: string[];
  tenantId: string;
  hospitalId: string;
  principalId: string;
  idpRoles: string[];
  /** targetRef -> sample trusted attributes, supplied directly rather
   * than resolved from a real resource. */
  sampleAttributes: Record<string, Record<string, unknown>>;
}

export interface LeafDecision {
  resource: string;
  action: string;
  target: string;
  allowed: boolean;
  reason: string;
}

export interface CapabilityResult {
  allowed: boolean;
  reason?: string;
  failedRequirements?: { resource: string; action: string; target: string; reason: string }[];
}

export interface SimulateCapabilitiesResult {
  authorizationRevision: number;
  rootPolicyRevision: string;
  capabilityCatalogRevision: string;
  capabilities: Record<string, CapabilityResult>;
  /** The full per-leaf requirement tree - administration-audience
   * evidence only (§12.4). */
  requirementTree: LeafDecision[];
}

/**
 * `POST /admin/authz/simulate` and `POST /admin/authz/simulate-capabilities`
 * (§9.4, issue #19): the effective-access simulator, running through the
 * real ADS and Cerbos path for an explicitly named principal.
 */
@Injectable({ providedIn: 'root' })
export class SimulatorApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  simulateAccess(request: SimulateAccessRequest): Observable<SimulateAccessResult> {
    return this.http.post<SimulateAccessResult>(`${this.adminBaseUrl}/authz/simulate`, request);
  }

  simulateCapabilities(request: SimulateCapabilitiesRequest): Observable<SimulateCapabilitiesResult> {
    return this.http.post<SimulateCapabilitiesResult>(
      `${this.adminBaseUrl}/authz/simulate-capabilities`,
      request,
    );
  }
}
