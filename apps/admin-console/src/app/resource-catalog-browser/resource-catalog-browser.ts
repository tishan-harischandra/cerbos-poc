import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';

import { CapabilityImpactApi, CapabilityRef } from '../capability-impact-api';
import { ResourceCatalogApi, ResourceEntry } from '../resource-catalog';

interface DomainGroup {
  domain: string;
  resources: ResourceEntry[];
}

/**
 * The resource catalog and capability impact modules (§6.1, §9.1, issue
 * #18): browse resources by domain with risk metadata and the current
 * catalog revision, then select a resource-action to see every composite
 * UI capability that depends on it.
 */
@Component({
  standalone: true,
  imports: [FormsModule],
  selector: 'app-resource-catalog-browser',
  templateUrl: './resource-catalog-browser.html',
  styleUrl: './resource-catalog-browser.css',
})
export class ResourceCatalogBrowser {
  private readonly catalogApi = inject(ResourceCatalogApi);
  private readonly impactApi = inject(CapabilityImpactApi);

  readonly resources = signal<ResourceEntry[]>([]);
  readonly rootPolicyRevision = signal('');
  readonly loadError = signal<string | null>(null);
  readonly filter = signal('');

  readonly selectedResourceKey = signal('');
  readonly selectedActionKey = signal('');
  readonly impactCapabilities = signal<CapabilityRef[] | null>(null);
  readonly impactLoading = signal(false);
  readonly impactError = signal<string | null>(null);

  readonly domains = computed<DomainGroup[]>(() => {
    const filter = this.filter().trim().toLowerCase();
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
    void this.loadCatalog();
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
        action.key.toLowerCase().includes(filter) || action.displayName.toLowerCase().includes(filter),
    );
  }

  private async loadCatalog(): Promise<void> {
    try {
      const catalog = await firstValueFrom(this.catalogApi.getResourceCatalog());
      this.resources.set(catalog.resources);
      this.rootPolicyRevision.set(catalog.rootPolicyRevision);
    } catch {
      this.loadError.set('The resource catalog could not be loaded.');
    }
  }

  async selectAction(resourceKey: string, actionKey: string): Promise<void> {
    this.selectedResourceKey.set(resourceKey);
    this.selectedActionKey.set(actionKey);
    this.impactCapabilities.set(null);
    this.impactError.set(null);
    this.impactLoading.set(true);
    try {
      const result = await firstValueFrom(this.impactApi.getCapabilityImpact(resourceKey, actionKey));
      // §18's "clearly shown as such rather than as an empty error": an
      // empty array is a valid, successful answer, not the absence of one.
      this.impactCapabilities.set(result.capabilities);
    } catch {
      this.impactError.set('The capability impact could not be loaded.');
    } finally {
      this.impactLoading.set(false);
    }
  }
}
