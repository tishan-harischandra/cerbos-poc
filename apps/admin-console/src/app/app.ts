import { Component } from '@angular/core';
import { PlatformStatus } from './platform-status/platform-status';

@Component({
  imports: [PlatformStatus],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected title = 'admin-console';
}
