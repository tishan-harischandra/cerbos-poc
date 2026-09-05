import { decodeAccessToken } from './token-claims';

function fakeJwt(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `${encode({ alg: 'RS256', typ: 'JWT' })}.${encode(payload)}.signature`;
}

describe('decodeAccessToken', () => {
  it('extracts the tenant, hospital and realm roles from a token', () => {
    const token = fakeJwt({
      sub: 'user-1',
      preferred_username: 'doctor',
      iss: 'https://localhost:8443/realms/tenant-a',
      organization: ['hospital-1'],
      exp: 1893456000,
      realm_access: { roles: ['doctor'] },
    });

    const claims = decodeAccessToken(token, 'patient-app');

    expect(claims.subject).toEqual('user-1');
    expect(claims.username).toEqual('doctor');
    expect(claims.tenantId).toEqual('tenant-a');
    expect(claims.hospitalId).toEqual('hospital-1');
    expect(claims.roles).toEqual(['doctor']);
    expect(claims.expiresAt).toEqual(1893456000);
  });

  it('derives the tenant from the realm in the issuer, not a tenant_id claim (ADR-010)', () => {
    const token = fakeJwt({ iss: 'http://keycloak:8080/realms/tenant-b' });

    expect(decodeAccessToken(token, 'patient-app').tenantId).toEqual('tenant-b');
  });

  it('names no tenant when the issuer has no /realms/ segment', () => {
    const token = fakeJwt({ iss: 'not-an-issuer-url' });

    expect(decodeAccessToken(token, 'patient-app').tenantId).toEqual('');
  });

  it('derives the active hospital from the organization claim, not a hospital_id claim (issue #78)', () => {
    const token = fakeJwt({ organization: ['north-hospital'] });

    expect(decodeAccessToken(token, 'patient-app').hospitalId).toEqual('north-hospital');
  });

  it('names no active hospital when the organization claim names more than one alias', () => {
    const token = fakeJwt({ organization: ['north-hospital', 'south-hospital'] });

    expect(decodeAccessToken(token, 'patient-app').hospitalId).toEqual('');
  });

  it('names no active hospital when the organization claim is absent', () => {
    const token = fakeJwt({});

    expect(decodeAccessToken(token, 'patient-app').hospitalId).toEqual('');
  });

  it('reads the realm role claim regardless of the configured client', () => {
    const token = fakeJwt({
      realm_access: { roles: ['doctor'] },
      resource_access: { 'another-app': { roles: ['administrator'] } },
    });

    const claims = decodeAccessToken(token, 'patient-app');

    expect(claims.roles).toEqual(['doctor']);
  });

  it('rejects a value that is not a three-segment JWT', () => {
    expect(() => decodeAccessToken('not-a-jwt', 'patient-app')).toThrow();
  });

  it('reads the tenant-wide realm role as isAdministrator (issue #78/#82)', () => {
    const token = fakeJwt({ realm_access: { roles: ['admin'] } });

    expect(decodeAccessToken(token, 'patient-app').isAdministrator).toBe(true);
  });

  it('is not an administrator when the realm role is absent', () => {
    const token = fakeJwt({ realm_access: { roles: ['some-other-role'] } });

    expect(decodeAccessToken(token, 'patient-app').isAdministrator).toBe(false);
  });

  it('is not an administrator when realm_access is absent entirely', () => {
    const token = fakeJwt({});

    expect(decodeAccessToken(token, 'patient-app').isAdministrator).toBe(false);
  });

  it('reports no other hospitals when the memberships claim is absent (issue #84)', () => {
    const token = fakeJwt({ organization: ['north-hospital'] });

    expect(decodeAccessToken(token, 'patient-app').otherHospitals).toEqual([]);
  });

  it('excludes the active hospital from otherHospitals even though Keycloak includes it', () => {
    const token = fakeJwt({
      organization: ['north-hospital'],
      organization_memberships: ['north-hospital', 'south-hospital'],
    });

    expect(decodeAccessToken(token, 'patient-app').otherHospitals).toEqual(['south-hospital']);
  });

  it('reports every membership when there is no active hospital (a tenant-wide session)', () => {
    const token = fakeJwt({
      organization_memberships: ['north-hospital', 'south-hospital'],
    });

    expect(decodeAccessToken(token, 'patient-app').otherHospitals).toEqual([
      'north-hospital',
      'south-hospital',
    ]);
  });
});
