/**
 * PKCE (RFC 7636) helpers for the Admin Console's OIDC login (§9's "Admin
 * Console shell, navigation and OIDC login", issue #16).
 *
 * The console is a public client (§7.1: "Public, so it holds no secret"):
 * it has no client secret to protect the authorization code exchange, so
 * PKCE is what stands in for one. Everything here is pure and testable
 * without a network call or a real browser redirect.
 */

const CODE_VERIFIER_LENGTH = 64;
const UNRESERVED_CHARS =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~';

/** Generates a cryptographically random RFC 7636 code_verifier. */
export function generateCodeVerifier(): string {
  const bytes = new Uint8Array(CODE_VERIFIER_LENGTH);
  crypto.getRandomValues(bytes);
  let verifier = '';
  for (const byte of bytes) {
    verifier += UNRESERVED_CHARS[byte % UNRESERVED_CHARS.length];
  }
  return verifier;
}

/** Generates an opaque random state/nonce value for CSRF protection. */
export function generateState(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}

/**
 * Derives the S256 code_challenge from a code_verifier: base64url(sha256(verifier)),
 * per RFC 7636 §4.2.
 */
export async function deriveCodeChallenge(verifier: string): Promise<string> {
  const encoded = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest('SHA-256', encoded);
  return base64UrlEncode(new Uint8Array(digest));
}

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
