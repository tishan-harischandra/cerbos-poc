package keycloakbulkload_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/keycloakbulkload"
	"golang.org/x/crypto/argon2"
)

func TestNewSharedCredentialProducesAValueKeycloaksArgon2ProviderAccepts(t *testing.T) {
	cred, err := keycloakbulkload.NewSharedCredential("P@ssw0rd!")
	if err != nil {
		t.Fatalf("NewSharedCredential: %v", err)
	}

	var secretData struct {
		Value string `json:"value"`
		Salt  string `json:"salt"`
	}
	if err := json.Unmarshal([]byte(cred.SecretData), &secretData); err != nil {
		t.Fatalf("decoding secret_data: %v", err)
	}

	var credentialData struct {
		HashIterations int    `json:"hashIterations"`
		Algorithm      string `json:"algorithm"`
	}
	if err := json.Unmarshal([]byte(cred.CredentialData), &credentialData); err != nil {
		t.Fatalf("decoding credential_data: %v", err)
	}
	if credentialData.Algorithm != "argon2" {
		t.Errorf("algorithm = %q, want argon2", credentialData.Algorithm)
	}

	salt, err := base64.StdEncoding.DecodeString(secretData.Salt)
	if err != nil {
		t.Fatalf("decoding salt: %v", err)
	}
	wantHash := argon2.IDKey([]byte("P@ssw0rd!"), salt, 1, 7168, 1, 32)
	gotHash, err := base64.StdEncoding.DecodeString(secretData.Value)
	if err != nil {
		t.Fatalf("decoding hash: %v", err)
	}
	if string(gotHash) != string(wantHash) {
		t.Error("the stored hash does not match Argon2id(password, salt) at Keycloak's documented parameters; " +
			"a real login against this row would fail to verify")
	}
}

func TestNewSharedCredentialRejectsAnEmptyPassword(t *testing.T) {
	if _, err := keycloakbulkload.NewSharedCredential(""); err == nil {
		t.Error("NewSharedCredential accepted an empty password")
	}
}

func TestNewSharedCredentialProducesADifferentSaltEachCall(t *testing.T) {
	a, err := keycloakbulkload.NewSharedCredential("P@ssw0rd!")
	if err != nil {
		t.Fatalf("NewSharedCredential: %v", err)
	}
	b, err := keycloakbulkload.NewSharedCredential("P@ssw0rd!")
	if err != nil {
		t.Fatalf("NewSharedCredential: %v", err)
	}
	if a.SecretData == b.SecretData {
		t.Error("two calls produced the same secret_data; the salt is not being randomised")
	}
}
