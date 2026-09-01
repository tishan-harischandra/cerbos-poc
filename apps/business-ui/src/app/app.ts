import { Component, inject } from '@angular/core';
import { RouterModule } from '@angular/router';

import { AuthService } from './auth/auth.service';

/**
 * The business UI's root shell: minimal by design (issue #12), plus the
 * one piece of chrome every route needs regardless of module - the
 * active hospital, shown prominently at all times, and a switcher driven
 * by the token's own other memberships (issue #84). Every other
 * capability-rendering mechanism from §12 is demonstrated by the routed
 * pages beneath it.
 */
@Component({
  imports: [RouterModule],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected readonly auth = inject(AuthService);

  /**
   * Switches hospital from the switcher's own select element. A failed
   * switch leaves the active hospital exactly where it was (issue #84);
   * the select resets to reflect that rather than showing a choice that
   * never took effect.
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
