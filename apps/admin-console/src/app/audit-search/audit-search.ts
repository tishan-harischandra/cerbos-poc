import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import { AuditEventRow, AuditSearchApi } from './audit-search-api';

const PAGE_LIMIT = 20;

/**
 * A `<input type="datetime-local">` value carries no timezone offset at
 * all (e.g. "2026-06-01T12:00"), but the server's `from`/`to` filters
 * require a full RFC3339 timestamp. Converting here, once, is what keeps
 * every date an administrator actually picks from failing the server's
 * "from must be an RFC3339 timestamp" check on every search.
 */
function toRFC3339(localValue: string): string | undefined {
  if (!localValue) {
    return undefined;
  }
  const parsed = new Date(localValue);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}

/**
 * The Admin Console's audit module (§9.1, §9.4, issue #20): search the
 * append-only administration audit by actor, role, target user, resource,
 * action, hospital and date range, and page through the results. There is
 * no edit or delete affordance anywhere in this screen - the audit is
 * append-only, and the console offers nothing that would suggest
 * otherwise.
 */
@Component({
  standalone: true,
  imports: [FormsModule],
  selector: 'app-audit-search',
  templateUrl: './audit-search.html',
  styleUrl: './audit-search.css',
})
export class AuditSearch {
  private readonly api = inject(AuditSearchApi);
  private readonly auth = inject(AuthService);

  /** Fixed by the administrator's own token scope, the same as every
   * other administration screen (§9.4's authority check). */
  readonly tenant = computed(() => this.auth.claims()?.tenantId ?? '');

  readonly hospital = signal('');
  readonly actor = signal('');
  readonly role = signal('');
  readonly user = signal('');
  readonly resource = signal('');
  readonly action = signal('');
  readonly from = signal('');
  readonly to = signal('');

  readonly offset = signal(0);
  readonly events = signal<AuditEventRow[]>([]);
  readonly totalCount = signal(0);
  readonly searchError = signal<string | null>(null);
  readonly searching = signal(false);

  readonly hasMore = computed(() => this.offset() + PAGE_LIMIT < this.totalCount());

  async search(resetOffset = true): Promise<void> {
    if (resetOffset) {
      this.offset.set(0);
    }
    this.searching.set(true);
    this.searchError.set(null);
    try {
      const result = await firstValueFrom(
        this.api.search(this.tenant(), {
          hospital: this.hospital() || undefined,
          actor: this.actor() || undefined,
          role: this.role() || undefined,
          user: this.user() || undefined,
          resource: this.resource() || undefined,
          action: this.action() || undefined,
          from: toRFC3339(this.from()),
          to: toRFC3339(this.to()),
          limit: PAGE_LIMIT,
          offset: this.offset(),
        }),
      );
      this.events.set(result.events);
      this.totalCount.set(result.totalCount);
    } catch {
      this.events.set([]);
      this.totalCount.set(0);
      this.searchError.set('The audit search failed.');
    } finally {
      this.searching.set(false);
    }
  }

  async nextPage(): Promise<void> {
    this.offset.set(this.offset() + PAGE_LIMIT);
    await this.search(false);
  }

  async previousPage(): Promise<void> {
    this.offset.set(Math.max(0, this.offset() - PAGE_LIMIT));
    await this.search(false);
  }
}
