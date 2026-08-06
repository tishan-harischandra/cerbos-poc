import { Component, inject } from '@angular/core';
import { RouterLink } from '@angular/router';
import { CapabilityDirective } from '@cerbos-poc/capability';

import { PatientsApi } from './patients-api';

/**
 * PatientsList demonstrates §12.7's row-level composite decision: every
 * row's "edit" menu item is rendered from the decision the list response
 * already carried, with no per-row capability request and no per-row
 * Cerbos call.
 */
@Component({
  standalone: true,
  imports: [RouterLink, CapabilityDirective],
  templateUrl: './patients-list.html',
})
export class PatientsList {
  private readonly api = inject(PatientsApi);
  protected readonly rows = this.api.list();
}
