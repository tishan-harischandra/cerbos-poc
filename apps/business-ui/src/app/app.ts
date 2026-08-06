import { Component } from '@angular/core';
import { RouterModule } from '@angular/router';

/**
 * The business UI's root shell: minimal by design (issue #12). It exists
 * to host the router outlet; every capability-rendering mechanism from
 * §12 is demonstrated by the routed pages beneath it.
 */
@Component({
  imports: [RouterModule],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {}
