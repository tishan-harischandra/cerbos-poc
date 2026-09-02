import { Component, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import {
  OrganizationMemberRef,
  OrganizationRef,
  OrganizationsApi,
} from './organizations-api';

const DEFAULT_PAGE_LIMIT = 20;

/**
 * The organizations module (§9.1, issue #85): a tenant's organizations and
 * an organization's members, read only - Keycloak stays the only place
 * one is created or changed. An administrator granting a permission needs
 * to see the reach of what they are about to do; this is where they see it.
 */
@Component({
  standalone: true,
  imports: [],
  selector: 'app-organizations',
  templateUrl: './organizations.html',
  styleUrl: './organizations.css',
})
export class Organizations {
  private readonly api = inject(OrganizationsApi);

  readonly organizations = signal<OrganizationRef[]>([]);
  readonly offset = signal(0);
  readonly hasMore = signal(false);
  readonly loadError = signal<string | null>(null);

  readonly selectedOrganization = signal<OrganizationRef | null>(null);
  readonly members = signal<OrganizationMemberRef[]>([]);
  readonly membersOffset = signal(0);
  readonly membersHasMore = signal(false);
  readonly membersError = signal<string | null>(null);

  constructor() {
    void this.loadOrganizations();
  }

  async loadOrganizations(): Promise<void> {
    this.loadError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.getOrganizations(this.offset(), DEFAULT_PAGE_LIMIT),
      );
      this.organizations.set(result.items);
      this.hasMore.set(result.hasMore);
    } catch {
      this.loadError.set('The organization list could not be loaded.');
    }
  }

  async nextPage(): Promise<void> {
    this.offset.set(this.offset() + DEFAULT_PAGE_LIMIT);
    await this.loadOrganizations();
  }

  async previousPage(): Promise<void> {
    this.offset.set(Math.max(0, this.offset() - DEFAULT_PAGE_LIMIT));
    await this.loadOrganizations();
  }

  async selectOrganization(organization: OrganizationRef): Promise<void> {
    this.selectedOrganization.set(organization);
    this.membersOffset.set(0);
    await this.loadMembers();
  }

  async loadMembers(): Promise<void> {
    const organization = this.selectedOrganization();
    if (!organization) {
      return;
    }
    this.membersError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.getOrganizationMembers(organization.externalId, this.membersOffset(), DEFAULT_PAGE_LIMIT),
      );
      this.members.set(result.items);
      this.membersHasMore.set(result.hasMore);
    } catch {
      this.membersError.set("The organization's members could not be loaded.");
    }
  }

  async membersNextPage(): Promise<void> {
    this.membersOffset.set(this.membersOffset() + DEFAULT_PAGE_LIMIT);
    await this.loadMembers();
  }

  async membersPreviousPage(): Promise<void> {
    this.membersOffset.set(Math.max(0, this.membersOffset() - DEFAULT_PAGE_LIMIT));
    await this.loadMembers();
  }
}
