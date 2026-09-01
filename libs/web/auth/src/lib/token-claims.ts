/**
 * The subset of an access token's claims the console reads for display
 * purposes (§16.1's tenant_id/hospital_id claims, §7.3's client role
 * claim). Nothing here is a security decision: the backend independently
 * verifies every claim on every request regardless of what the browser
 * decoded.
 */
export interface TokenClaims {
  subject: string;
  username: string;
  tenantId: string;
  hospitalId: string;
  roles: string[];
  expiresAt: number;
  /**
   * Whether the token carries the tenant-wide realm role (issue #78's
   * `admin`), read for display purposes only - e.g. issue #82's link to
   * Keycloak's own administration console. Every administrative call
   * still goes through the server, which verifies this independently.
   */
  isAdministrator: boolean;
  /**
   * Every hospital the user belongs to other than the active one (issue
   * #84), for a hospital switcher. Display data only: it drives which
   * options the switcher offers, never a decision - the backend's own
   * `tests/architecture` check is what keeps it that way on the server
   * side, and nothing in this module reads it to decide anything either.
   */
  otherHospitals: string[];
}

/** Decodes a JWT's payload without verifying its signature. */
export function decodeAccessToken(token: string, clientId: string): TokenClaims {
  const parts = token.split('.');
  if (parts.length !== 3) {
    throw new Error('not a JWT: expected three dot-separated segments');
  }
  const payload = JSON.parse(base64UrlDecode(parts[1])) as Record<string, unknown>;

  const resourceAccess = (payload['resource_access'] ?? {}) as Record<
    string,
    { roles?: string[] }
  >;
  const realmAccess = (payload['realm_access'] ?? {}) as { roles?: string[] };
  const hospitalId = String(payload['hospital_id'] ?? '');

  return {
    subject: String(payload['sub'] ?? ''),
    username: String(payload['preferred_username'] ?? ''),
    tenantId: String(payload['tenant_id'] ?? ''),
    hospitalId,
    roles: resourceAccess[clientId]?.roles ?? [],
    expiresAt: Number(payload['exp'] ?? 0),
    isAdministrator: (realmAccess.roles ?? []).includes('admin'),
    otherHospitals: otherHospitalsOf(payload, hospitalId),
  };
}

function otherHospitalsOf(payload: Record<string, unknown>, active: string): string[] {
  const memberships = payload['organization_memberships'];
  if (!Array.isArray(memberships)) {
    return [];
  }
  return memberships.filter(
    (alias): alias is string => typeof alias === 'string' && alias !== active,
  );
}

function base64UrlDecode(segment: string): string {
  const padded = segment.replace(/-/g, '+').replace(/_/g, '/');
  return atob(padded);
}
