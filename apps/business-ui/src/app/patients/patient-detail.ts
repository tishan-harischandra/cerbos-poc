import { Component, inject, OnInit } from '@angular/core';
import { ActivatedRoute, RouterLink, RouterOutlet } from '@angular/router';
import { CapabilitySnapshotService } from '@cerbos-poc/capability';

/**
 * PatientDetail fetches the instance-context snapshot exactly once, when
 * the page resource loads (§12.6). Its child routes (overview, edit) are
 * rendered through this component's own <router-outlet> and read the
 * CapabilityStore the fetch already populated - navigating between them
 * issues no further capability request.
 */
@Component({
  standalone: true,
  imports: [RouterLink, RouterOutlet],
  templateUrl: './patient-detail.html',
})
export class PatientDetail implements OnInit {
  private readonly route = inject(ActivatedRoute);
  private readonly snapshots = inject(CapabilitySnapshotService);

  protected patientId = '';

  ngOnInit(): void {
    this.patientId = this.route.snapshot.paramMap.get('id') ?? '';
    void this.snapshots.loadInstance(
      'clinical',
      ['patient.route.details', 'patient.route.edit'],
      `patient:${this.patientId}`,
      { patientId: this.patientId },
    );
  }
}
