/**
 * CapabilityDecision is the browser-facing shape of one capability's
 * evaluated outcome (§12.4, §12.5). `failedRequirements` is optional
 * evidence the ADS attaches only for the administration audience; an
 * end-user response carries `reason` alone.
 */
export interface CapabilityDecision {
  allowed: boolean;
  reason?: string;
  failedRequirements?: FailedRequirement[];
}

export interface FailedRequirement {
  resource: string;
  action: string;
  target: string;
  reason: string;
}

/**
 * UiCapabilitySnapshot is the §12.4 capability snapshot shape, as received
 * from the ADS's capability evaluator.
 */
export interface UiCapabilitySnapshot {
  authorizationRevision: number;
  rootPolicyRevision?: string;
  capabilityCatalogRevision: string;
  tenantId?: string;
  hospitalId?: string;
  module: string;
  contextFingerprint: string;
  capabilities: Record<string, CapabilityDecision>;
}
