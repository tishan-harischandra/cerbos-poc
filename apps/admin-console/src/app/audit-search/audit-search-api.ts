import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';

/** One administration audit event (§9.1, §9.4, §17.3). */
export interface AuditEventRow {
  eventId: string;
  actorId: string;
  operation: string;
  targetType: string;
  before: string;
  after: string;
  tenantId: string;
  hospitalId?: string;
  roleExternalId?: string;
  targetUserId?: string;
  resourceActionKeys?: string;
  correlationId?: string;
  createdAt: string;
}

export interface AuditSearchResult {
  events: AuditEventRow[];
  totalCount: number;
}

/** Every filter GET /admin/authz/audit accepts (§9.4). tenant is supplied
 * separately by the caller, the same mandatory predicate the server itself
 * enforces (§8.2). */
export interface AuditSearchFilters {
  hospital?: string;
  actor?: string;
  role?: string;
  user?: string;
  resource?: string;
  action?: string;
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

@Injectable({ providedIn: 'root' })
export class AuditSearchApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  search(tenant: string, filters: AuditSearchFilters): Observable<AuditSearchResult> {
    const params: Record<string, string> = { tenant };
    if (filters.hospital) params['hospital'] = filters.hospital;
    if (filters.actor) params['actor'] = filters.actor;
    if (filters.role) params['role'] = filters.role;
    if (filters.user) params['user'] = filters.user;
    if (filters.resource) params['resource'] = filters.resource;
    if (filters.action) params['action'] = filters.action;
    if (filters.from) params['from'] = filters.from;
    if (filters.to) params['to'] = filters.to;
    if (filters.limit !== undefined) params['limit'] = String(filters.limit);
    if (filters.offset !== undefined) params['offset'] = String(filters.offset);

    return this.http.get<AuditSearchResult>(`${this.adminBaseUrl}/authz/audit`, { params });
  }
}
