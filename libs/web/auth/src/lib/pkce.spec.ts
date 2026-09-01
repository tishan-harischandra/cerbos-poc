import {
  deriveCodeChallenge,
  generateCodeVerifier,
  generateState,
} from './pkce';

describe('pkce', () => {
  it('generates a code verifier using only the RFC 7636 unreserved character set', () => {
    const verifier = generateCodeVerifier();
    expect(verifier.length).toBeGreaterThanOrEqual(43);
    expect(verifier).toMatch(/^[A-Za-z0-9\-._~]+$/);
  });

  it('generates a different code verifier on every call', () => {
    expect(generateCodeVerifier()).not.toEqual(generateCodeVerifier());
  });

  it('generates a different state value on every call', () => {
    expect(generateState()).not.toEqual(generateState());
  });

  it('derives the same code challenge for the same verifier', async () => {
    const verifier = generateCodeVerifier();
    const first = await deriveCodeChallenge(verifier);
    const second = await deriveCodeChallenge(verifier);
    expect(first).toEqual(second);
  });

  it('derives a code challenge with no base64 padding or URL-unsafe characters', async () => {
    const challenge = await deriveCodeChallenge('a-known-verifier-value');
    expect(challenge).not.toContain('=');
    expect(challenge).not.toContain('+');
    expect(challenge).not.toContain('/');
  });

  it('derives different challenges for different verifiers', async () => {
    const a = await deriveCodeChallenge('verifier-one');
    const b = await deriveCodeChallenge('verifier-two');
    expect(a).not.toEqual(b);
  });
});
