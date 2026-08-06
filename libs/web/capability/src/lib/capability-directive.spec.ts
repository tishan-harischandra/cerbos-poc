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

@Component({
  standalone: true,
  imports: [CapabilityDirective],
  template: `<span
    *capability="'patient.row.edit'; decisions: rowDecisions"
    >edit</span
  >`,
})
class RowHostComponent {
  rowDecisions: Record<string, { allowed: boolean }> = {
    'patient.row.edit': { allowed: true },
  };
}

describe('CapabilityDirective row-level decisions', () => {
  it('renders from an explicit local decision map with no store and no HTTP call', () => {
    TestBed.configureTestingModule({ imports: [RowHostComponent] });
    // Deliberately never replace anything into CapabilityStore: the row
    // decision map must be self-sufficient (§12.7 - render row menus from
    // a single batched response with no per-row request).

    const fixture = TestBed.createComponent(RowHostComponent);
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('edit');
  });

  it('hides the row control when the local decision map denies it', () => {
    TestBed.configureTestingModule({ imports: [RowHostComponent] });
    const fixture = TestBed.createComponent(RowHostComponent);
    fixture.componentInstance.rowDecisions = {
      'patient.row.edit': { allowed: false },
    };
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).not.toContain('edit');
  });
});
