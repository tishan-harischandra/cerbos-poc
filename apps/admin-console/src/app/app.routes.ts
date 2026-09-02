import { Route } from '@angular/router';

import { AuditSearch } from './audit-search/audit-search';
import { authGuard } from './auth/auth.guard';
import { Callback } from './auth/callback';
import { IdPDiagnostics } from './idp-diagnostics/idp-diagnostics';
import { Organizations } from './organizations/organizations';
import { ResourceCatalogBrowser } from './resource-catalog-browser/resource-catalog-browser';
import { RevisionActivation } from './revision-activation/revision-activation';
import { RoleMatrix } from './role-matrix/role-matrix';
import { Shell } from './shell/shell';
import { Simulator } from './simulator/simulator';
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
      { path: 'organizations', component: Organizations },
      { path: 'resource-catalog', component: ResourceCatalogBrowser },
      { path: 'simulator', component: Simulator },
      { path: 'audit', component: AuditSearch },
      { path: 'revision-activation', component: RevisionActivation },
      { path: 'idp-diagnostics', component: IdPDiagnostics },
    ],
  },
];
