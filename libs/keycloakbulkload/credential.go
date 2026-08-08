package keycloakbulkload

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// LoadTestPasswordPolicy is the realm password policy this package's
// credential rows are computed against: Argon2id at the minimum work factor
// Keycloak 26.4 accepts. It must be set on the load realm before login is
// attempted against rows this package writes, or Keycloak's own login path
// will hash an operator-entered password at a different work factor than the
// one baked into every seeded row and every login will fail to verify.
//
// This is deliberately far cheaper than a production password policy: at
// full population scale (600,000 users) computing even a moderate-cost
// Argon2id hash per user is minutes of CPU the load harness has no reason to
// spend, since the password is a fixed, documented, load-test-only value
// shared by the whole population (see SharedCredential). A production
// installation must never use this policy or reuse this package's output.
const LoadTestPasswordPolicy = "hashIterations(1)"

// argon2Time, argon2MemoryKiB and argon2Parallelism are the Argon2id
// parameters LoadTestPasswordPolicy above maps to in Keycloak 26.4's default
// Argon2 credential provider. hashLength and the "id"/"1.3" markers below are
// that provider's fixed defaults, not configurable via the password policy.
const (
	argon2Time        = 1
	argon2MemoryKiB   = 7168
	argon2Parallelism = 1
	argon2HashLength  = 32
)

// SharedCredential is one Argon2id password credential, computed once and
// reused verbatim for every seeded user (only the credential row's id and
// user_id differ per user). Computing a fresh hash for 600,000 users would
// cost real, unnecessary CPU time in a load harness whose whole population
// shares one documented, load-test-only password; this is that trade-off
// made explicit rather than hidden.
type SharedCredential struct {
	// Password is the load-test-only plaintext, needed by anything that logs
	// a seeded user in (e.g. the token-size measurement in issue #24's
	// acceptance criteria). Never reused outside a throwaway load
	// environment.
	Password string
	// SecretData and CredentialData are the exact JSON strings Keycloak's
	// credential table expects (its CredentialModel encoding), precomputed
	// once so BulkLoad's hot path is a string copy per user, not a hash.
	SecretData     string
	CredentialData string
}

// NewSharedCredential computes one Argon2id credential in
// LoadTestPasswordPolicy's shape for the given plaintext password.
func NewSharedCredential(password string) (SharedCredential, error) {
	if password == "" {
		return SharedCredential{}, fmt.Errorf("keycloakbulkload: a password is required")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return SharedCredential{}, fmt.Errorf("keycloakbulkload: generating a salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryKiB, argon2Parallelism, argon2HashLength)

	secretData, err := json.Marshal(map[string]any{
		"value":                base64.StdEncoding.EncodeToString(hash),
		"salt":                 base64.StdEncoding.EncodeToString(salt),
		"additionalParameters": map[string]any{},
	})
	if err != nil {
		return SharedCredential{}, fmt.Errorf("keycloakbulkload: encoding secret_data: %w", err)
	}

	credentialData, err := json.Marshal(map[string]any{
		"hashIterations": argon2Time,
		"algorithm":      "argon2",
		"additionalParameters": map[string]any{
			"hashLength":  []string{fmt.Sprintf("%d", argon2HashLength)},
			"memory":      []string{fmt.Sprintf("%d", argon2MemoryKiB)},
			"type":        []string{"id"},
			"version":     []string{"1.3"},
			"parallelism": []string{fmt.Sprintf("%d", argon2Parallelism)},
		},
	})
	if err != nil {
		return SharedCredential{}, fmt.Errorf("keycloakbulkload: encoding credential_data: %w", err)
	}

	return SharedCredential{
		Password:       password,
		SecretData:     string(secretData),
		CredentialData: string(credentialData),
	}, nil
}
