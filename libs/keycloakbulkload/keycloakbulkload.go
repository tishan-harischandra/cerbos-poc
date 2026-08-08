// Package keycloakbulkload writes the load-test population (issue #24, §15
// load model) directly into Keycloak's own internal database schema.
//
// Keycloak's Admin REST API cannot reach the target population in a bounded
// time. A spike measured partialImport batching (the fastest REST-shaped
// path: whole users, 70 roles inline, in one request) at ~4 users/sec and
// ~280 role-mapping rows/sec against a real Postgres-backed Keycloak 26.4 -
// roughly 42 hours to seed 600,000 users and 42,000,000 role mappings,
// because Keycloak still does one JPA persist per row underneath the batched
// HTTP call. Direct SQL, using PostgreSQL's COPY protocol, is the only path
// that reaches a documented, usable seed duration.
//
// This is an explicitly reviewed coupling (marked HITL on the issue), and it
// is confined to this package on purpose:
//
//   - KeycloakVersion below is the exact version this package's SQL is
//     verified against. USER_ENTITY, CREDENTIAL, USER_ATTRIBUTE,
//     USER_ROLE_MAPPING and KEYCLOAK_ROLE are Keycloak-internal tables with
//     no compatibility guarantee across releases; upgrading the Keycloak
//     image requires re-verifying every column this package writes against
//     the new schema before this package is trusted again.
//   - Everything that does not need bulk throughput - creating the load
//     realm, the client and the 250-role catalog - goes through the ordinary
//     Admin REST API instead (roles.go), so the raw-SQL surface is as small
//     as it can be.
//   - Nothing outside this package writes to Keycloak's schema directly.
package keycloakbulkload

// KeycloakVersion is the exact Keycloak image tag this package's direct-SQL
// writer is verified against (docker-compose.yml's keycloak-loadtest
// service). The schema of USER_ENTITY, CREDENTIAL, USER_ATTRIBUTE,
// USER_ROLE_MAPPING and KEYCLOAK_ROLE was inspected against a running
// instance of exactly this version; nothing here is derived from Keycloak's
// public API or documentation, because there isn't one for these tables.
const KeycloakVersion = "26.4"
