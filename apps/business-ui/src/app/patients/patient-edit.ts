import { HttpClient, HttpContext, HttpErrorResponse } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { CAPABILITY_INSTANCE_KEY } from '@cerbos-poc/capability';

type SaveStatus = 'idle' | 'saving' | 'saved' | 'denied';

/**
 * PatientEdit demonstrates §12.6's stale-snapshot recovery end to end: a
 * business call that returns 403 is retried exactly once, through
 * capabilityRetryInterceptor (registered globally in app.config.ts), and
 * a denial that survives the retry is shown as final - never retried
 * again.
 */
@Component({
  standalone: true,
  templateUrl: './patient-edit.html',
})
export class PatientEdit {
  private readonly http = inject(HttpClient);
  private readonly route = inject(ActivatedRoute);

  private readonly patientId =
    this.route.parent?.snapshot.paramMap.get('id') ?? '';

  protected readonly status = signal<SaveStatus>('idle');

  save(): void {
    this.status.set('saving');
    this.http
      .patch(`/api/business/patients/${this.patientId}`, {}, {
        context: new HttpContext().set(
          CAPABILITY_INSTANCE_KEY,
          `patient:${this.patientId}`,
        ),
      })
      .subscribe({
        next: () => this.status.set('saved'),
        error: (error: unknown) => {
          if (error instanceof HttpErrorResponse && error.status === 403) {
            this.status.set('denied');
            return;
          }
          throw error;
        },
      });
  }
}
