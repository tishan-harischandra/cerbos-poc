import { Injectable } from '@angular/core';
import { CapabilityDecision } from '@cerbos-poc/capability';

export interface PatientRow {
  id: string;
  name: string;
  capabilities: Record<string, CapabilityDecision>;
}

/**
 * PatientsApi stands in for the FHIR resource endpoints a later issue
 * will add (apps/resource-service has no patient_record route yet). It
 * demonstrates §12.7's row-level batch decision shape: the list response
 * already carries each row's composite decision, computed once
 * server-side, so the list page renders row menus with zero further
 * capability or Cerbos requests.
 */
@Injectable({ providedIn: 'root' })
export class PatientsApi {
  list(): PatientRow[] {
    return [
      {
        id: 'patient-456',
        name: 'Jordan Rivers',
        capabilities: { 'patient.row.edit': { allowed: true } },
      },
      {
        id: 'patient-457',
        name: 'Sam Okafor',
        capabilities: {
          'patient.row.edit': {
            allowed: false,
            reason: 'REQUIRED_PERMISSION_DENIED',
          },
        },
      },
    ];
  }
}
