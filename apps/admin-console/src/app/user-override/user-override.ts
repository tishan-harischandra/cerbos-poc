import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import { ResourceCatalogApi, ResourceEntry } from '../resource-catalog';
import {
  OverrideEffectInput,
  OverrideRow,
  PreviewResult,
  UserOverrideApi,
  UserRef,
  UserRoleRef,
} from './user-override-api';

const DEFAULT_PAGE_LIMIT = 20;

/**
 * The user-override screen (§9.1, §9.3, issue #17): the tri-state
 * INHERIT/GRANT/REVOKE control, with a side-by-side role-result and
 * effective-result preview before saving.
 */
@Component({
  standalone: true,
  imports: [FormsModule],
  selector: 'app-user-override',
  templateUrl: './user-override.html',
  styleUrl: './user-override.css',
})
export class UserOverride {
  private readonly api = inject(UserOverrideApi);
  private readonly catalogApi = inject(ResourceCatalogApi);
  private readonly auth = inject(AuthService);

  /**
   * Both are fixed by the administrator's own token scope (§9.4's
   * authority check is tenant *and* hospital for this screen) - there is
   * nothing to pick beyond what the token already names, and the screen
   * says so.
   */
  readonly tenant = computed(() => this.auth.claims()?.tenantId ?? '');
  readonly hospital = computed(() => this.auth.claims()?.hospitalId ?? '');

  readonly userQuery = signal('');
  readonly userOffset = signal(0);
  readonly users = signal<UserRef[]>([]);
  readonly userHasMore = signal(false);
  readonly userSearchError = signal<string | null>(null);

  readonly selectedUser = signal<UserRef | null>(null);
  private readonly userRoles = signal<UserRoleRef[]>([]);

  readonly resources = signal<ResourceEntry[]>([]);
  readonly selectedResourceKey = signal('');
  readonly selectedActionKey = signal('');

  readonly effect = signal<OverrideEffectInput>('INHERIT');
  readonly reason = signal('');
  readonly validFrom = signal('');
  readonly validUntil = signal('');

  readonly existingOverrides = signal<OverrideRow[]>([]);
  readonly preview = signal<PreviewResult | null>(null);
  readonly previewError = signal<string | null>(null);

  readonly saving = signal(false);
  readonly saveError = signal<string | null>(null);
  readonly staleRevision = signal(false);
  private lastKnownRevision = 0;
  readonly appliedValidUntil = signal<string | null>(null);

  readonly selectedResource = computed<ResourceEntry | undefined>(() =>
    this.resources().find((r) => r.resourceKey === this.selectedResourceKey()),
  );

  constructor() {
    void this.loadResourceCatalog();
  }

  private async loadResourceCatalog(): Promise<void> {
    try {
      const catalog = await firstValueFrom(this.catalogApi.getResourceCatalog());
      this.resources.set(catalog.resources);
    } catch {
      // The resource picker degrades to empty; the rest of the screen
      // still functions for a user already selected via a different flow.
    }
  }

  async searchUsers(resetOffset = true): Promise<void> {
    if (resetOffset) {
      this.userOffset.set(0);
    }
    this.userSearchError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.searchUsers(this.userQuery(), this.userOffset(), DEFAULT_PAGE_LIMIT),
      );
      this.users.set(result.items);
      this.userHasMore.set(result.hasMore);
    } catch {
      this.userSearchError.set('User search failed.');
    }
  }

  async nextPage(): Promise<void> {
    this.userOffset.set(this.userOffset() + DEFAULT_PAGE_LIMIT);
    await this.searchUsers(false);
  }

  async previousPage(): Promise<void> {
    this.userOffset.set(Math.max(0, this.userOffset() - DEFAULT_PAGE_LIMIT));
    await this.searchUsers(false);
  }

  async selectUser(user: UserRef): Promise<void> {
    this.selectedUser.set(user);
    this.saveError.set(null);
    this.staleRevision.set(false);
    this.preview.set(null);

    // Both requests are dispatched together rather than one after the
    // other: neither depends on the other's result, and awaiting them in
    // sequence would double the round-trip latency for no reason.
    const rolesRequest = firstValueFrom(this.api.getUserRoles(user.externalId)).catch(
      () => ({ items: [] as UserRoleRef[] }),
    );
    const revisionRequest = firstValueFrom(this.api.getCurrentRevision(this.tenant())).catch(
      () => ({ revision: 0 }),
    );

    const [roles, current] = await Promise.all([rolesRequest, revisionRequest]);
    this.userRoles.set(roles.items);
    this.lastKnownRevision = current.revision;
  }

  async selectResource(resourceKey: string): Promise<void> {
    this.selectedResourceKey.set(resourceKey);
    this.selectedActionKey.set('');
    this.preview.set(null);
    await this.loadExistingOverrides();
  }

  private async loadExistingOverrides(): Promise<void> {
    const user = this.selectedUser();
    const resourceKey = this.selectedResourceKey();
    if (!user || !resourceKey) {
      return;
    }
    try {
      const result = await firstValueFrom(
        this.api.getOverrides(this.tenant(), this.hospital(), user.externalId, resourceKey),
      );
      this.existingOverrides.set(result.overrides);
    } catch {
      this.existingOverrides.set([]);
    }
  }

  /**
   * Is the row for the currently selected action, if any, still within
   * its validity window at this instant? Distinguishes an active
   * override from an expired one (AC "An expired override is visibly
   * distinguished from an active one") without waiting on a reconciler:
   * the read is what already excludes it from ActiveUserOverrides on the
   * server, but the *raw* row list here is shown so the administrator can
   * still see history.
   */
  isActive(row: OverrideRow): boolean {
    const now = new Date();
    if (new Date(row.validFrom) > now) {
      return false;
    }
    if (row.validUntil && new Date(row.validUntil) <= now) {
      return false;
    }
    return true;
  }

  async runPreview(): Promise<void> {
    const resourceKey = this.selectedResourceKey();
    const actionKey = this.selectedActionKey();
    if (!resourceKey || !actionKey) {
      return;
    }
    const user = this.selectedUser();
    if (!user) {
      return;
    }
    this.previewError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.preview(this.tenant(), this.hospital(), user.externalId, {
          resourceKey,
          actionKey,
          effect: this.effect(),
          roleExternalIds: this.userRoles().map((r) => r.canonicalId).filter(Boolean),
        }),
      );
      this.preview.set(result);
    } catch {
      this.previewError.set('The preview could not be computed.');
    }
  }

  /** §9.3: "An override without a reason is rejected" - a GRANT or REVOKE
   * always needs one; INHERIT clears a row and needs nothing. */
  canSave(): boolean {
    if (!this.selectedUser() || !this.selectedResourceKey() || !this.selectedActionKey()) {
      return false;
    }
    if (this.effect() === 'INHERIT') {
      return true;
    }
    return this.reason().trim() !== '' && this.validFrom() !== '';
  }

  async save(): Promise<void> {
    const user = this.selectedUser();
    if (!user || !this.canSave()) {
      return;
    }
    this.saving.set(true);
    this.saveError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.saveOverride(this.tenant(), this.hospital(), user.externalId, {
          expectedRevision: this.lastKnownRevision,
          resourceKey: this.selectedResourceKey(),
          actionKey: this.selectedActionKey(),
          effect: this.effect(),
          reason: this.reason(),
          validFrom: this.validFrom() || undefined,
          validUntil: this.validUntil() || undefined,
          roleExternalIds: this.userRoles().map((r) => r.canonicalId).filter(Boolean),
        }),
      );
      this.lastKnownRevision = result.revision;
      this.staleRevision.set(false);
      this.appliedValidUntil.set(result.appliedValidUntil ?? null);
      this.preview.set({
        roleResult: result.roleResult,
        effectiveResult: result.effectiveResult,
        noPracticalEffect: result.noPracticalEffect,
      });
      // Fired without blocking save()'s own completion: refreshing the
      // history list is a courtesy, not part of the save outcome the
      // caller is waiting on.
      void this.loadExistingOverrides();
    } catch (err) {
      if (err instanceof HttpErrorResponse && err.status === 409) {
        this.staleRevision.set(true);
      } else {
        this.saveError.set('Saving the override failed.');
      }
    } finally {
      this.saving.set(false);
    }
  }

  async reload(): Promise<void> {
    await this.loadExistingOverrides();
    this.staleRevision.set(false);
  }
}
