import { Component } from '@angular/core';
import { RouterOutlet } from '@angular/router';

/**
 * The application root: hosts the router outlet only. The shell (nav,
 * identity, logout) and the platform-status readout both live behind
 * routes now (issue #16), rather than being wired directly here.
 */
@Component({
  imports: [RouterOutlet],
  selector: 'app-root',
  templateUrl: './app.html',
})
export class App {}
