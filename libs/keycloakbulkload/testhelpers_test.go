package keycloakbulkload_test

import (
	"crypto/sha1" //nolint:gosec // deterministic test fixture id, not a security use
	"encoding/base64"
	"fmt"
)

// deterministicTestUUID derives a UUID-shaped string for a test fixture, the
// same way the package's own deterministic ids work, so integration test
// fixtures do not depend on a random UUID generator being available.
func deterministicTestUUID(seed string) string {
	h := sha1.New() //nolint:gosec // deterministic test fixture id, not a security use
	h.Write([]byte(seed))
	sum := h.Sum(nil)
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// base64URLDecode decodes a JWT segment, which uses base64url without
// padding.
func base64URLDecode(segment string) ([]byte, error) {
	for len(segment)%4 != 0 {
		segment += "="
	}
	return base64.URLEncoding.DecodeString(segment)
}
