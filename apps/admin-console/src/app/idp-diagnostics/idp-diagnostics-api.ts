import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ADMIN_BASE_URL } from '../admin-base-url';

/** The selected identity provider's configuration and live connectivity
 * (§9.1, issue #22). */
export interface IdPDiagnosticsResult {
  provider: string;
  roleSource: string;
  connectivity: 'ok' | 'degraded';
}

@Injectable({ providedIn: 'root' })
export class IdPDiagnosticsApi {
  private readonly http = inject(HttpClient);
  private readonly adminBaseUrl = inject(ADMIN_BASE_URL);

  getDiagnostics(): Observable<IdPDiagnosticsResult> {
    return this.http.get<IdPDiagnosticsResult>(`${this.adminBaseUrl}/idp/diagnostics`);
  }
}
