import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';
import { ADS_BASE_URL } from '../platform-status/ads-base-url';

/** A user as the identity directory reports it (§7.2). */
export interface UserRef {
  externalId: string;
  username: string;
  displayName: string;
  email: string;
  enabled: boolean;
}

export interface UserSearchResult {
  items: UserRef[];
  offset: number;
  limit: number;
  hasMore: boolean;
}

/** A role directly assigned to a user, from the same authoritative source
 * the role matrix screen searches (§7.5). */
export interface UserRoleRef {
  canonicalId: string;
  externalId: string;
  name: string;
  description: string;
}

export interface UserRolesResult {
  items: UserRoleRef[];
}

/** An organization a user belongs to (issue #85): the reach an
 * administrator sees before granting or revoking a permission. */
export interface UserOrganizationRef {
  externalId: string;
  alias: string;
  name: string;
}

export interface UserOrganizationsResult {
  items: UserOrganizationRef[];
}

export type OverrideEffectInput = 'INHERIT' | 'GRANT' | 'REVOKE';

export interface SaveOverrideRequest {
  expectedRevision: number;
  resourceKey: string;
  actionKey: string;
  effect: OverrideEffectInput;
  reason: string;
  validFrom?: string;
  validUntil?: string;
  roleExternalIds: string[];
}

export interface SaveOverrideResult {
  revision: number;
  roleResult: boolean;
  effectiveResult: boolean;
  noPracticalEffect: boolean;
  /** Present only when the server applied a default expiry the request
   * did not name (§9.3's bounded default for a high-risk action). */
  appliedValidUntil?: string;
}

export interface OverrideRow {
  actionKey: string;
  effect: string;
  enabled: boolean;
  reason?: string;
  validFrom: string;
  validUntil?: string;
  revision: number;
}

export interface OverridesResult {
  overrides: OverrideRow[];
}

export interface PreviewRequest {
  resourceKey: string;
  actionKey: string;
  effect: OverrideEffectInput;
  roleExternalIds: string[];
}

export interface PreviewResult {
  roleResult: boolean;
  effectiveResult: boolean;
  noPracticalEffect: boolean;
}

@Injectable({ providedIn: 'root' })
export class UserOverrideApi {
  private readonly http = inject(HttpClient);
  private readonly adsBaseUrl = inject(ADS_BASE_URL);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  searchUsers(query: string, offset: number, limit: number): Observable<UserSearchResult> {
    return this.http.get<UserSearchResult>(`${this.adsBaseUrl}/internal/directory/users`, {
      params: {
        ...(query ? { query } : {}),
        offset: String(offset),
        limit: String(limit),
      },
    });
  }

  getUserRoles(userExternalId: string): Observable<UserRolesResult> {
    return this.http.get<UserRolesResult>(
      `${this.adsBaseUrl}/internal/directory/users/${userExternalId}/roles`,
    );
  }

  getUserOrganizations(userExternalId: string): Observable<UserOrganizationsResult> {
    return this.http.get<UserOrganizationsResult>(
      `${this.adsBaseUrl}/internal/directory/users/${userExternalId}/organizations`,
    );
  }

  /**
   * The tenant-wide permission revision, the same one SaveRoleMatrix
   * advances (§10.1) and SaveUserOverrideWrite's expected-revision guard
   * checks against. Reusing rolematrix's own endpoint here: there is only
   * one such counter per tenant, not one per screen.
   */
  getCurrentRevision(tenant: string): Observable<{ revision: number }> {
    return this.http.get<{ revision: number }>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/permission-revision`,
    );
  }

  getOverrides(
    tenant: string,
    hospital: string,
    user: string,
    resource: string,
  ): Observable<OverridesResult> {
    return this.http.get<OverridesResult>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/hospitals/${hospital}/users/${user}/overrides`,
      { params: { resource } },
    );
  }

  saveOverride(
    tenant: string,
    hospital: string,
    user: string,
    request: SaveOverrideRequest,
  ): Observable<SaveOverrideResult> {
    return this.http.put<SaveOverrideResult>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/hospitals/${hospital}/users/${user}/overrides`,
      request,
    );
  }

  preview(
    tenant: string,
    hospital: string,
    user: string,
    request: PreviewRequest,
  ): Observable<PreviewResult> {
    return this.http.post<PreviewResult>(
      `${this.adminBaseUrl}/authz/tenants/${tenant}/hospitals/${hospital}/users/${user}/overrides/preview`,
      request,
    );
  }
}
