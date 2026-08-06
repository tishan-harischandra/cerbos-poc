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

  return {
    subject: String(payload['sub'] ?? ''),
    username: String(payload['preferred_username'] ?? ''),
    tenantId: String(payload['tenant_id'] ?? ''),
    hospitalId: String(payload['hospital_id'] ?? ''),
    roles: resourceAccess[clientId]?.roles ?? [],
    expiresAt: Number(payload['exp'] ?? 0),
  };
}

function base64UrlDecode(segment: string): string {
  const padded = segment.replace(/-/g, '+').replace(/_/g, '/');
  return atob(padded);
}
