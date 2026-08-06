import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';
import { ADS_BASE_URL } from '../platform-status/ads-base-url';

/** A role as the identity directory reports it (§7.5, §9.2). */
export interface RoleRef {
  /**
   * The stable identifier the role matrix persists against. Empty when
   * the directory could not resolve exactly one canonical role for this
   * entry - an inherited, composite, or otherwise ambiguous IdP role
   * (§9.2's "Show inherited or composite IdP roles as informational
   * context" and the role matrix module's "flag an unresolved canonical
   * role for remediation"): the console's own display metadata is the
   * only signal it has for either case, so both share one treatment
   * here.
   */
  canonicalId: string;
  externalId: string;
  name: string;
  description: string;
}

export interface RoleSearchResult {
  items: RoleRef[];
}

export interface ActionEntry {
  key: string;
  displayName: string;
  context: string;
}

export interface ResourceEntry {
  resourceKey: string;
  displayName: string;
  domain: string;
  actions: ActionEntry[];
}

export interface ResourceCatalog {
  resources: ResourceEntry[];
  rootPolicyRevision: string;
}

export interface PermissionRow {
  resourceKey: string;
  actionKey: string;
  enabled: boolean;
  validFrom: string;
  validUntil?: string;
}

export interface RoleMatrix {
  permissions: PermissionRow[];
  revision: number;
}

export interface SaveResult {
  revision: number;
}

/** Thrown by save() on a 409, carrying nothing else: the caller already
 * has everything it needs to offer a reload. */
export class StaleRevisionError extends Error {}

@Injectable({ providedIn: 'root' })
export class RoleMatrixApi {
  private readonly http = inject(HttpClient);
  private readonly adsBaseUrl = inject(ADS_BASE_URL);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  searchRoles(query: string): Observable<RoleSearchResult> {
    return this.http.get<RoleSearchResult>(`${this.adsBaseUrl}/internal/directory/roles`, {
      params: query ? { query } : {},
    });
  }

  getResourceCatalog(): Observable<ResourceCatalog> {
    return this.http.get<ResourceCatalog>(`${this.adminBaseUrl}/authz/resources`);
  }

  getRoleMatrix(tenant: string, role: string): Observable<RoleMatrix> {
    return this.http.get<RoleMatrix>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/roles/${role}/permissions`,
    );
  }

  saveRoleMatrix(
    tenant: string,
    role: string,
    expectedRevision: number,
    permissions: PermissionRow[],
  ): Observable<SaveResult> {
    return this.http.put<SaveResult>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/roles/${role}/permissions`,
      { expectedRevision, permissions },
    );
  }
}
