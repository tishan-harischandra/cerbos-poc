import { Route } from '@angular/router';
import { capabilityCanActivate } from '@cerbos-poc/capability';

import { Callback } from './auth/callback';
import { sessionGuard } from './auth/session.guard';
import { SigningIn } from './auth/signing-in';
import { Forbidden } from './forbidden';
import { PatientDetail } from './patients/patient-detail';
import { PatientEdit } from './patients/patient-edit';
import { PatientOverview } from './patients/patient-overview';
import { PatientsList } from './patients/patients-list';

// The session is established in canMatch and the capability checked in
// canActivate, and the split is load-bearing rather than stylistic: the
// router invokes every canMatch guard on a route eagerly and
// concurrently, so a capability check sitting second in that same array
// would read the store before the snapshot it depends on had arrived and
// send a fully-permitted user to /forbidden. Recognition (canMatch)
// finishes before the canActivate phase starts, so this ordering is the
// one the router actually guarantees.
//
// /callback and /signing-in are deliberately unguarded - they are the
// two routes a user is on while still logged out.
export const appRoutes: Route[] = [
  { path: '', redirectTo: 'patients', pathMatch: 'full' },
  { path: 'callback', component: Callback },
  { path: 'signing-in', component: SigningIn },
  {
    path: 'patients',
    children: [
      {
        path: '',
        pathMatch: 'full',
        component: PatientsList,
        canMatch: [sessionGuard],
        canActivate: [capabilityCanActivate],
        data: { capability: 'patients.route.list' },
      },
      {
        path: ':id',
        component: PatientDetail,
        canMatch: [sessionGuard],
        canActivate: [capabilityCanActivate],
        data: { capability: 'patient.route.details' },
        children: [
          { path: '', pathMatch: 'full', component: PatientOverview },
          {
            path: 'edit',
            component: PatientEdit,
            canActivate: [capabilityCanActivate],
            data: { capability: 'patient.route.edit' },
          },
        ],
      },
    ],
  },
  { path: 'forbidden', component: Forbidden },
];
