import { Component, inject } from '@angular/core';
import { RouterLink, RouterOutlet } from '@angular/router';

import { AuthService } from '../auth/auth.service';
import { PlatformStatus } from '../platform-status/platform-status';

/**
 * The Admin Console's shell and navigation (§9's "Admin Console shell,
 * navigation and OIDC login", issue #16). Only the Role matrix module is
 * routed yet; the other §9.1 modules join the nav as their own issues land.
 * Carries the platform-status readout that used to be the whole app.
 */
@Component({
  standalone: true,
  imports: [RouterLink, RouterOutlet, PlatformStatus],
  selector: 'app-shell',
  templateUrl: './shell.html',
  styleUrl: './shell.css',
})
export class Shell {
  protected readonly auth = inject(AuthService);

  logout(): void {
    this.auth.logout();
  }

  /**
   * Switches hospital from the switcher's own select element (issue #84).
   * A failed switch - the target is not one of the user's memberships, or
   * the silent request otherwise cannot be satisfied - leaves the active
   * hospital exactly where it was; the select resets to reflect that
   * rather than showing a choice that never took effect.
   */
  async onSwitchHospital(event: Event): Promise<void> {
    const select = event.target as HTMLSelectElement;
    const organization = select.value;
    select.value = '';
    if (organization) {
      await this.auth.switchHospital(organization);
    }
  }
}
