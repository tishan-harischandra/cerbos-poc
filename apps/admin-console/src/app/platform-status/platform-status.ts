import { HttpClient } from '@angular/common/http';
import { Component, inject, OnInit, signal } from '@angular/core';

import { ADS_BASE_URL } from './ads-base-url';

type Health = 'checking' | 'healthy' | 'unreachable';

@Component({
  selector: 'app-platform-status',
  standalone: true,
  templateUrl: './platform-status.html',
  styleUrl: './platform-status.css',
})
export class PlatformStatus implements OnInit {
  private readonly http = inject(HttpClient);
  private readonly adsBaseUrl = inject(ADS_BASE_URL);

  protected readonly ads = signal<Health>('checking');

  ngOnInit(): void {
    this.http.get<{ status: string }>(`${this.adsBaseUrl}/healthz`).subscribe({
      next: (body) => this.ads.set(body.status === 'ok' ? 'healthy' : 'unreachable'),
      error: () => this.ads.set('unreachable'),
    });
  }
}
