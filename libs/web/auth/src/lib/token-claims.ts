/**
 * The subset of an access token's claims the console reads for display
 * purposes: the realm in `iss` as the tenant (ADR-010), the organization
 * claim as the active hospital (§75), and the selected organization's
 * structured roles.
 * Nothing here is a security decision: the backend independently
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

/**
 * Decodes a JWT's payload without verifying its signature.
 *
 * Hospital-scoped roles come only from `organization_roles`; global realm and
 * receiving-client roles are used only when the token has no active hospital.
 * This mirrors the server's no-fallback organization-role contract.
 */
export function decodeAccessToken(token: string, clientId: string): TokenClaims {
  const parts = token.split('.');
  if (parts.length !== 3) {
    throw new Error('not a JWT: expected three dot-separated segments');
  }
  const payload = JSON.parse(base64UrlDecode(parts[1])) as Record<string, unknown>;

  const realmAccess = (payload['realm_access'] ?? {}) as { roles?: string[] };
  const resourceAccess = (payload['resource_access'] ?? {}) as Record<
    string,
    { roles?: string[] }
  >;
  const hospitalId = activeHospitalOf(payload);
  const globalRoles = [...(realmAccess.roles ?? []), ...(resourceAccess[clientId]?.roles ?? [])];

  return {
    subject: String(payload['sub'] ?? ''),
    username: String(payload['preferred_username'] ?? ''),
    tenantId: tenantIdOf(payload),
    hospitalId,
    roles: hospitalId ? organizationRolesOf(payload, clientId) : globalRoles,
    expiresAt: Number(payload['exp'] ?? 0),
    isAdministrator: (realmAccess.roles ?? []).includes('admin'),
    otherHospitals: otherHospitalsOf(payload, hospitalId),
  };
}

/**
 * A tenant is the Keycloak realm that signed the token, full stop - there
 * is no `tenant_id` claim and no mapping layer (ADR-010, deviation S8):
 * the server derives its own TenantID from the realm the verifying
 * Installation is configured for (libs/tokenverifier), never from a
 * token claim, and this mirrors that rather than reading one that does
 * not exist. `iss` is `<issuer base>/realms/<realm>` for every adapter
 * this platform ships (§7.1), so the realm is the segment after the
 * last `/realms/`.
 */
function tenantIdOf(payload: Record<string, unknown>): string {
  const issuer = String(payload['iss'] ?? '');
  const marker = '/realms/';
  const index = issuer.lastIndexOf(marker);
  return index === -1 ? '' : issuer.slice(index + marker.length);
}

function organizationRolesOf(payload: Record<string, unknown>, clientId: string): string[] {
  const claim = payload['organization_roles'];
  if (typeof claim !== 'object' || claim === null || Array.isArray(claim)) {
    return [];
  }
  const organizationRoles = claim as {
    realm?: unknown;
    client?: Record<string, unknown>;
  };
  const realm = Array.isArray(organizationRoles.realm)
    ? organizationRoles.realm.filter((role): role is string => typeof role === 'string')
    : [];
  const client = organizationRoles.client?.[clientId];
  const clientRoles = Array.isArray(client)
    ? client.filter((role): role is string => typeof role === 'string')
    : [];
  return [...realm, ...clientRoles];
}

/**
 * The active hospital is the token's organization claim (issue #78,
 * §75) - a JSON array of alias strings Keycloak's organization scope
 * itself produces, e.g. `"organization": ["north-hospital"]` - never a
 * `hospital_id` claim, which does not exist. Mirrors
 * libs/tokenverifier's own hospitalOf/organizationAliases: any other
 * shape (absent, empty, more than one alias, a non-string entry) is
 * "no active hospital" for display purposes, the same as the server
 * treats it as unscoped or ambiguous.
 */
function activeHospitalOf(payload: Record<string, unknown>): string {
  const raw = payload['organization'];
  if (!Array.isArray(raw) || raw.length !== 1 || typeof raw[0] !== 'string') {
    return '';
  }
  return raw[0];
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
