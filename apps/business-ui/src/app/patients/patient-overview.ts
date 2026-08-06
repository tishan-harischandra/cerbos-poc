import { Component, inject } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { CapabilityDirective } from '@cerbos-poc/capability';

/**
 * PatientOverview is a child route of PatientDetail. It renders using
 * the instance snapshot PatientDetail already fetched - reaching this
 * route issues no capability request of its own.
 */
@Component({
  standalone: true,
  imports: [CapabilityDirective],
  templateUrl: './patient-overview.html',
})
export class PatientOverview {
  private readonly route = inject(ActivatedRoute);
  protected readonly patientId =
    this.route.parent?.snapshot.paramMap.get('id') ?? '';
}
