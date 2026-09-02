// Deterministic per-VU identity, shared by every scenario so the same VU
// always authenticates as the same load-model user for the run's duration
// (one password grant per VU, per §15).
//
// tenantIndexFor/hospitalAliasesFor mirror libs/loadmodel/loadmodel.go's
// Population.User exactly (issue #87): same modulo-by-tenant-count
// assignment, same "start at a deterministic offset, step forward one
// hospital per membership" selection. A VU that computed its tenant or
// hospital any other way would authenticate as a real seeded user but
// request a scope or send resource attributes that user was never placed
// in, and the ADS would refuse or silently mis-scope every request.
import {
  usernameFor,
  TOTAL_LOAD_USERS,
  TENANTS,
  HOSPITALS_PER_TENANT,
  HOSPITALS_PER_USER,
  REALM_SUFFIX,
} from './config.js';

export function userIndexFor(counter) {
  return counter % TOTAL_LOAD_USERS;
}

export function tenantIndexFor(userIndex) {
  return userIndex % TENANTS;
}

export function tenantIdFor(userIndex) {
  return `tenant-${tenantIndexFor(userIndex) + 1}`;
}

export function realmFor(userIndex) {
  return `${tenantIdFor(userIndex)}${REALM_SUFFIX}`;
}

// hospitalAliasesFor returns exactly HOSPITALS_PER_USER distinct hospital
// aliases, HOSPITALS_PER_USER[0] this user's primary hospital - the same
// shape loadmodel.User.HospitalIDs/HospitalID() carries.
export function hospitalAliasesFor(userIndex) {
  const tenantId = tenantIdFor(userIndex);
  const base = Math.floor(userIndex / TENANTS) % HOSPITALS_PER_TENANT;
  const aliases = [];
  for (let k = 0; k < HOSPITALS_PER_USER; k++) {
    aliases.push(`${tenantId}-hospital-${((base + k) % HOSPITALS_PER_TENANT) + 1}`);
  }
  return aliases;
}

export function primaryHospitalFor(userIndex) {
  return hospitalAliasesFor(userIndex)[0];
}

// identityFor bundles every derived fact about one user index, so a
// scenario computes it once and passes it around rather than re-deriving
// each field separately.
export function identityFor(userIndex) {
  return {
    userIndex,
    username: usernameFor(userIndex),
    tenantId: tenantIdFor(userIndex),
    realm: realmFor(userIndex),
    hospitalId: primaryHospitalFor(userIndex),
    hospitalAliases: hospitalAliasesFor(userIndex),
  };
}

// __VU is unique across every active VU in the run, including across
// scenarios executing concurrently, so this never collides between the
// business, capability and token-baseline scenario pools.
export function identityForVU() {
  return identityFor(userIndexFor(__VU));
}

// usernameForVU is kept for any caller that only ever needed the
// username; identityForVU is the one every scenario needing a realm or
// hospital scope should use instead.
export function usernameForVU() {
  return usernameFor(userIndexFor(__VU));
}
