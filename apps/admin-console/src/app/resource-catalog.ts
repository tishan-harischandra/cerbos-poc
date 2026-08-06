import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from './admin-base-url';

/** One action a resource declares in the administration-facing catalog. */
export interface ActionEntry {
  key: string;
  displayName: string;
  context: string;
}

/** One resource's full administration-facing catalog entry (§9.1, §9.2). */
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

/**
 * `GET /admin/authz/resources`, shared by the role matrix (issue #16) and
 * user override (issue #17) screens - both browse the same catalog to pick
 * a resource and action, and neither should hold its own copy of the
 * fetch.
 */
@Injectable({ providedIn: 'root' })
export class ResourceCatalogApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  getResourceCatalog(): Observable<ResourceCatalog> {
    return this.http.get<ResourceCatalog>(`${this.adminBaseUrl}/authz/resources`);
  }
}
