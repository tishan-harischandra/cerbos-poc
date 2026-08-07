import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import {
  LeafDecision,
  SimulateAccessResult,
  SimulateCapabilitiesResult,
  SimulatorApi,
} from './simulator-api';

function parseList(raw: string): string[] {
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '');
}

function parseJSONObject(raw: string): Record<string, unknown> {
  if (raw.trim() === '') {
    return {};
  }
  const parsed = JSON.parse(raw);
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new Error('expected a JSON object');
  }
  return parsed as Record<string, unknown>;
}

/**
 * The effective-access simulator (§9.4, §12.4, issue #19): answers "why
 * can this person do this" by running the real ADS and Cerbos path for
 * an explicitly named principal - never a client-side reimplementation
 * of precedence.
 */
@Component({
  standalone: true,
  imports: [FormsModule],
  selector: 'app-simulator',
  templateUrl: './simulator.html',
  styleUrl: './simulator.css',
})
export class Simulator {
  private readonly api = inject(SimulatorApi);
  private readonly auth = inject(AuthService);

  /** Fixed by the administrator's own token scope, the same authority
   * check every other admin-service write/simulate endpoint enforces. */
  readonly tenant = computed(() => this.auth.claims()?.tenantId ?? '');
  readonly hospital = computed(() => this.auth.claims()?.hospitalId ?? '');

  // --- Access simulation ---
  readonly principalId = signal('');
  readonly idpRoles = signal('');
  readonly resourceKind = signal('');
  readonly resourceId = signal('');
  readonly resourceAttributes = signal('{"status": "ACTIVE"}');
  readonly action = signal('');

  readonly accessResult = signal<SimulateAccessResult | null>(null);
  readonly accessError = signal<string | null>(null);
  readonly accessRunning = signal(false);

  async runAccessSimulation(): Promise<void> {
    this.accessError.set(null);
    this.accessResult.set(null);

    let attributes: Record<string, unknown>;
    try {
      attributes = parseJSONObject(this.resourceAttributes());
    } catch {
      this.accessError.set('Resource attributes must be a JSON object.');
      return;
    }

    this.accessRunning.set(true);
    try {
      const result = await firstValueFrom(
        this.api.simulateAccess({
          tenantId: this.tenant(),
          hospitalId: this.hospital(),
          principalId: this.principalId(),
          idpRoles: parseList(this.idpRoles()),
          resource: { kind: this.resourceKind(), id: this.resourceId(), attributes },
          action: this.action(),
        }),
      );
      this.accessResult.set(result);
    } catch {
      this.accessError.set('The simulation could not be run.');
    } finally {
      this.accessRunning.set(false);
    }
  }

  // --- Capability simulation ---
  readonly capabilityModule = signal('');
  readonly capabilityKeys = signal('');
  readonly capabilityPrincipalId = signal('');
  readonly capabilityIdpRoles = signal('');
  readonly sampleAttributesJSON = signal('{"patient": {"status": "ACTIVE"}}');

  readonly capabilityResult = signal<SimulateCapabilitiesResult | null>(null);
  readonly capabilityError = signal<string | null>(null);
  readonly capabilityRunning = signal(false);

  async runCapabilitySimulation(): Promise<void> {
    this.capabilityError.set(null);
    this.capabilityResult.set(null);

    let sampleAttributes: Record<string, Record<string, unknown>>;
    try {
      const raw = this.sampleAttributesJSON().trim();
      sampleAttributes = raw === '' ? {} : (JSON.parse(raw) as Record<string, Record<string, unknown>>);
    } catch {
      this.capabilityError.set('Sample attributes must be a JSON object of targetRef to attributes.');
      return;
    }

    this.capabilityRunning.set(true);
    try {
      const result = await firstValueFrom(
        this.api.simulateCapabilities({
          module: this.capabilityModule(),
          capabilityKeys: parseList(this.capabilityKeys()),
          tenantId: this.tenant(),
          hospitalId: this.hospital(),
          principalId: this.capabilityPrincipalId(),
          idpRoles: parseList(this.capabilityIdpRoles()),
          sampleAttributes,
        }),
      );
      this.capabilityResult.set(result);
    } catch {
      this.capabilityError.set('The simulation could not be run.');
    } finally {
      this.capabilityRunning.set(false);
    }
  }

  /** Every leaf's decision, allowed and denied alike - administrators
   * only (§12.4). */
  requirementTreeFor(result: SimulateCapabilitiesResult): LeafDecision[] {
    return result.requirementTree;
  }

  /** Angular templates cannot call Object.keys/entries directly, so this
   * projects the capabilities map into an iterable array. */
  capabilityEntries(result: SimulateCapabilitiesResult): { key: string; allowed: boolean }[] {
    return Object.entries(result.capabilities).map(([key, value]) => ({ key, allowed: value.allowed }));
  }
}
