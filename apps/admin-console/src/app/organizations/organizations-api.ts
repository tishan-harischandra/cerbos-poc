import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADS_BASE_URL } from '../platform-status/ads-base-url';

/** An organization as the identity directory reports it (issue #85). */
export interface OrganizationRef {
  externalId: string;
  alias: string;
  name: string;
}

export interface OrganizationsResult {
  items: OrganizationRef[];
  offset: number;
  limit: number;
  hasMore: boolean;
}

/** A member of an organization, the same shape the user picker already
 * shows elsewhere in the console (§7.2). */
export interface OrganizationMemberRef {
  externalId: string;
  username: string;
  displayName: string;
  email: string;
  enabled: boolean;
}

export interface OrganizationMembersResult {
  items: OrganizationMemberRef[];
  offset: number;
  limit: number;
  hasMore: boolean;
}

@Injectable({ providedIn: 'root' })
export class OrganizationsApi {
  private readonly http = inject(HttpClient);
  private readonly adsBaseUrl = inject(ADS_BASE_URL);

  getOrganizations(offset: number, limit: number): Observable<OrganizationsResult> {
    return this.http.get<OrganizationsResult>(`${this.adsBaseUrl}/internal/directory/organizations`, {
      params: { offset: String(offset), limit: String(limit) },
    });
  }

  getOrganizationMembers(
    organizationExternalId: string,
    offset: number,
    limit: number,
  ): Observable<OrganizationMembersResult> {
    return this.http.get<OrganizationMembersResult>(
      `${this.adsBaseUrl}/internal/directory/organizations/${organizationExternalId}/members`,
      { params: { offset: String(offset), limit: String(limit) } },
    );
  }
}
