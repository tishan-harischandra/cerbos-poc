import { Route } from '@angular/router';
import { capabilityGuard } from '@cerbos-poc/capability';

import { Forbidden } from './forbidden';
import { PatientDetail } from './patients/patient-detail';
import { PatientEdit } from './patients/patient-edit';
import { PatientOverview } from './patients/patient-overview';
import { PatientsList } from './patients/patients-list';

export const appRoutes: Route[] = [
  { path: '', redirectTo: 'patients', pathMatch: 'full' },
  {
    path: 'patients',
    children: [
      {
        path: '',
        pathMatch: 'full',
        component: PatientsList,
        canMatch: [capabilityGuard],
        data: { capability: 'patients.route.list' },
      },
      {
        path: ':id',
        component: PatientDetail,
        canMatch: [capabilityGuard],
        data: { capability: 'patient.route.details' },
        children: [
          { path: '', component: PatientOverview },
          {
            path: 'edit',
            component: PatientEdit,
            canMatch: [capabilityGuard],
            data: { capability: 'patient.route.edit' },
          },
        ],
      },
    ],
  },
  { path: 'forbidden', component: Forbidden },
];
