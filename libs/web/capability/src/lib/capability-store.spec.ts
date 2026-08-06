import { CapabilityStore } from './capability-store';
import { UiCapabilitySnapshot } from './capability-decision';

describe('CapabilityStore', () => {
  it('reports a capability as not allowed before any snapshot has loaded', () => {
    const store = new CapabilityStore();

    expect(store.can('patient.route.details')).toBe(false);
  });

  it('reports a capability as allowed once a snapshot granting it is replaced in', () => {
    const store = new CapabilityStore();
    const snapshot: UiCapabilitySnapshot = {
      authorizationRevision: 1,
      capabilityCatalogRevision: 'ui-capabilities-v1',
      module: 'clinical',
      contextFingerprint: 'sha256:abc',
      capabilities: {
        'patient.route.details': { allowed: true },
        'patient.route.edit': { allowed: false, reason: 'REQUIRED_PERMISSION_DENIED' },
      },
    };

    store.replace(snapshot);

    expect(store.can('patient.route.details')).toBe(true);
    expect(store.can('patient.route.edit')).toBe(false);
  });
});
