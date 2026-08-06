import { Route } from '@angular/router';

import { authGuard } from './auth/auth.guard';
import { Callback } from './auth/callback';
import { ResourceCatalogBrowser } from './resource-catalog-browser/resource-catalog-browser';
import { RoleMatrix } from './role-matrix/role-matrix';
import { Shell } from './shell/shell';
import { UserOverride } from './user-override/user-override';

export const appRoutes: Route[] = [
  { path: 'callback', component: Callback },
  {
    path: '',
    component: Shell,
    canActivate: [authGuard],
    children: [
      { path: '', redirectTo: 'role-matrix', pathMatch: 'full' },
      { path: 'role-matrix', component: RoleMatrix },
      { path: 'user-overrides', component: UserOverride },
      { path: 'resource-catalog', component: ResourceCatalogBrowser },
    ],
  },
];
