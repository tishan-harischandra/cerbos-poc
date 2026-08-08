// Token acquisition and refresh-token rotation (§15: "each VU performs one
// password grant at start, then refresh-token rotation"). Every scenario
// shares this so identity cost is exactly one password grant per VU, ever -
// everything after that is a refresh, exactly like the token-baseline
// scenario measures in isolation.
import http from 'k6/http';
import { check } from 'k6';
import { KEYCLOAK_URL, REALM, CLIENT_ID, PASSWORD, TOKEN_REUSE_SECONDS } from './config.js';

const TOKEN_URL = `${KEYCLOAK_URL}/realms/${REALM}/protocol/openid-connect/token`;

// passwordGrant exchanges a username/password for a token pair. Called
// exactly once per VU per scenario (in that scenario's setup/first
// iteration), never per request.
export function passwordGrant(username) {
  const res = http.post(
    TOKEN_URL,
    {
      grant_type: 'password',
      client_id: CLIENT_ID,
      username,
      password: PASSWORD,
    },
    { tags: { name: 'token_password_grant' } }
  );
  check(res, { 'password grant returned a token': (r) => r.status === 200 && !!r.json('access_token') });
  return tokenSetFrom(res);
}

// refreshGrant rotates a refresh token for a new token pair without a
// password round trip.
export function refreshGrant(refreshToken) {
  const res = http.post(
    TOKEN_URL,
    {
      grant_type: 'refresh_token',
      client_id: CLIENT_ID,
      refresh_token: refreshToken,
    },
    { tags: { name: 'token_refresh_grant' } }
  );
  check(res, { 'refresh grant returned a token': (r) => r.status === 200 && !!r.json('access_token') });
  return tokenSetFrom(res);
}

function tokenSetFrom(res) {
  if (res.status !== 200) {
    return null;
  }
  const body = res.json();
  return {
    accessToken: body.access_token,
    refreshToken: body.refresh_token,
    obtainedAt: Date.now(),
    expiresInSeconds: body.expires_in,
  };
}

// ensureFreshToken returns a token set for `username`, minting one with a
// password grant on first use and rotating it with a refresh grant once it
// is within TOKEN_REUSE_SECONDS of expiry - the "refresh is a small fraction
// of request volume" property, driven by the real per-VU call rate rather
// than a fixed schedule.
export function ensureFreshToken(username, current) {
  if (!current) {
    return passwordGrant(username);
  }
  const ageSeconds = (Date.now() - current.obtainedAt) / 1000;
  if (ageSeconds < current.expiresInSeconds - TOKEN_REUSE_SECONDS) {
    return current;
  }
  const refreshed = refreshGrant(current.refreshToken);
  return refreshed || passwordGrant(username);
}

export function authHeader(tokenSet) {
  return { Authorization: `Bearer ${tokenSet.accessToken}` };
}
