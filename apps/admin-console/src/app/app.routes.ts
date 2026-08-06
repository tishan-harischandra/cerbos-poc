import { Route } from '@angular/router';

import { authGuard } from './auth/auth.guard';
import { Callback } from './auth/callback';
import { RoleMatrix } from './role-matrix/role-matrix';
import { Shell } from './shell/shell';

export const appRoutes: Route[] = [
  { path: 'callback', component: Callback },
  {
    path: '',
    component: Shell,
    canActivate: [authGuard],
    children: [
      { path: '', redirectTo: 'role-matrix', pathMatch: 'full' },
      { path: 'role-matrix', component: RoleMatrix },
    ],
  },
];
