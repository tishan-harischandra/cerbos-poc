import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { CapabilityDirective } from './capability-directive';
import { CapabilityStore } from './capability-store';
import { UiCapabilitySnapshot } from './capability-decision';

@Component({
  standalone: true,
  imports: [CapabilityDirective],
  template: `<span *capability="'patient.component.clinical-summary'">summary</span>`,
})
class HostComponent {}

function snapshotGranting(...keys: string[]): UiCapabilitySnapshot {
  const capabilities: UiCapabilitySnapshot['capabilities'] = {};
  for (const key of keys) capabilities[key] = { allowed: true };
  return {
    authorizationRevision: 1,
    capabilityCatalogRevision: 'ui-capabilities-v1',
    module: 'clinical',
    contextFingerprint: 'sha256:abc',
    capabilities,
  };
}

describe('CapabilityDirective', () => {
  it('renders its content when the store grants the named capability', () => {
    TestBed.configureTestingModule({ imports: [HostComponent] });
    TestBed.inject(CapabilityStore).replace(
      snapshotGranting('patient.component.clinical-summary'),
    );

    const fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('summary');
  });

  it('renders nothing when the store denies the named capability', () => {
    TestBed.configureTestingModule({ imports: [HostComponent] });
    TestBed.inject(CapabilityStore).replace(snapshotGranting());

    const fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).not.toContain('summary');
  });

  it('re-renders when a fresh snapshot changes the decision, with no page reload', () => {
    TestBed.configureTestingModule({ imports: [HostComponent] });
    const store = TestBed.inject(CapabilityStore);
    store.replace(snapshotGranting());

    const fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).not.toContain('summary');

    store.replace(snapshotGranting('patient.component.clinical-summary'));
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('summary');
  });
});
