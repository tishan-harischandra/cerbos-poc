import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import {
  PermissionRow,
  ResourceEntry,
  RoleMatrixApi,
  RoleRef,
} from './role-matrix-api';

interface DomainGroup {
  domain: string;
  resources: ResourceEntry[];
}

/**
 * The role permission matrix screen (§9.1, §9.2, issue #16): select a
 * tenant-scoped role, browse and search the full resource catalog, and
 * enable/disable resource-action grants with an expected-revision guard.
 */
@Component({
  standalone: true,
  imports: [FormsModule],
  selector: 'app-role-matrix',
  templateUrl: './role-matrix.html',
  styleUrl: './role-matrix.css',
})
export class RoleMatrix {
  private readonly api = inject(RoleMatrixApi);
  private readonly auth = inject(AuthService);

  /**
   * The role matrix is tenant-scoped by the administrator's own token
   * (§9.4's authority check); there is nothing to "select" here beyond
   * what the token already names.
   */
  readonly tenant = computed(() => this.auth.claims()?.tenantId ?? '');

  readonly roleQuery = signal('');
  readonly roles = signal<RoleRef[]>([]);
  readonly roleSearchError = signal<string | null>(null);

  readonly selectedRole = signal<RoleRef | null>(null);
  readonly resources = signal<ResourceEntry[]>([]);
  readonly resourceFilter = signal('');

  /** Keyed by `${resourceKey}:${actionKey}`, the checkbox grid's own state. */
  private readonly permissionsByKey = signal<Map<string, PermissionRow>>(new Map());
  readonly expectedRevision = signal(0);

  readonly loadError = signal<string | null>(null);
  readonly saving = signal(false);
  readonly staleRevision = signal(false);
  readonly saveError = signal<string | null>(null);

  readonly domains = computed<DomainGroup[]>(() => {
    const filter = this.resourceFilter().trim().toLowerCase();
    const byDomain = new Map<string, ResourceEntry[]>();
    for (const resource of this.resources()) {
      if (filter && !this.matchesFilter(resource, filter)) {
        continue;
      }
      const domain = resource.domain || 'other';
      const group = byDomain.get(domain) ?? [];
      group.push(resource);
      byDomain.set(domain, group);
    }
    return Array.from(byDomain.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([domain, resources]) => ({ domain, resources }));
  });

  constructor() {
    void this.loadResourceCatalog();
  }

  private matchesFilter(resource: ResourceEntry, filter: string): boolean {
    if (
      resource.resourceKey.toLowerCase().includes(filter) ||
      resource.displayName.toLowerCase().includes(filter) ||
      resource.domain.toLowerCase().includes(filter)
    ) {
      return true;
    }
    return resource.actions.some(
      (action) =>
        action.key.toLowerCase().includes(filter) ||
        action.displayName.toLowerCase().includes(filter),
    );
  }

  private async loadResourceCatalog(): Promise<void> {
    try {
      const catalog = await firstValueFrom(this.api.getResourceCatalog());
      this.resources.set(catalog.resources);
    } catch {
      this.loadError.set('The resource catalog could not be loaded.');
    }
  }

  async searchRoles(): Promise<void> {
    this.roleSearchError.set(null);
    try {
      const result = await firstValueFrom(this.api.searchRoles(this.roleQuery()));
      this.roles.set(result.items);
    } catch {
      this.roleSearchError.set('Role search failed.');
    }
  }

  /**
   * An unresolved role (§9.2) has no stable identifier to persist
   * assignments against, so it is never selectable here - only flagged.
   */
  canSelect(role: RoleRef): boolean {
    return role.canonicalId !== '';
  }

  async selectRole(role: RoleRef): Promise<void> {
    if (!this.canSelect(role)) {
      return;
    }
    this.selectedRole.set(role);
    this.saveError.set(null);
    this.staleRevision.set(false);
    await this.reloadMatrix();
  }

  private async reloadMatrix(): Promise<void> {
    const role = this.selectedRole();
    if (!role) {
      return;
    }
    this.loadError.set(null);
    try {
      const matrix = await firstValueFrom(this.api.getRoleMatrix(this.tenant(), role.canonicalId));
      const byKey = new Map<string, PermissionRow>();
      for (const row of matrix.permissions) {
        byKey.set(`${row.resourceKey}:${row.actionKey}`, row);
      }
      this.permissionsByKey.set(byKey);
      this.expectedRevision.set(matrix.revision);
      this.staleRevision.set(false);
    } catch {
      this.loadError.set("This role's permissions could not be loaded.");
    }
  }

  isEnabled(resourceKey: string, actionKey: string): boolean {
    return this.permissionsByKey().get(`${resourceKey}:${actionKey}`)?.enabled ?? false;
  }

  /**
   * Flips one checkbox. A cleared checkbox means no grant, never an
   * explicit deny (§8.3, §9.2) - toggling off simply removes this role's
   * contribution; it never creates a row that denies.
   */
  toggle(resourceKey: string, actionKey: string): void {
    const byKey = new Map(this.permissionsByKey());
    const key = `${resourceKey}:${actionKey}`;
    const existing = byKey.get(key);
    byKey.set(key, {
      resourceKey,
      actionKey,
      enabled: !(existing?.enabled ?? false),
      validFrom: existing?.validFrom ?? new Date().toISOString(),
      validUntil: existing?.validUntil,
    });
    this.permissionsByKey.set(byKey);
  }

  async save(): Promise<void> {
    const role = this.selectedRole();
    if (!role) {
      return;
    }
    this.saving.set(true);
    this.saveError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.saveRoleMatrix(
          this.tenant(),
          role.canonicalId,
          this.expectedRevision(),
          Array.from(this.permissionsByKey().values()),
        ),
      );
      this.expectedRevision.set(result.revision);
      this.staleRevision.set(false);
    } catch (err) {
      if (err instanceof HttpErrorResponse && err.status === 409) {
        // §9.2's "actionable error offering reload": the pending edits in
        // permissionsByKey are left exactly as the administrator made
        // them. Nothing here refetches or clears them - only an explicit
        // reload() call does, and only because the administrator asked
        // for it.
        this.staleRevision.set(true);
      } else {
        this.saveError.set('Saving the role matrix failed.');
      }
    } finally {
      this.saving.set(false);
    }
  }

  async reload(): Promise<void> {
    await this.reloadMatrix();
  }
}
