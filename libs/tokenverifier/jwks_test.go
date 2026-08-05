package tokenverifier_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

func TestTheKeySetIsFetchedFromTheIssuerAndAnswersByKeyID(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	server := jwksServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	defer server.Close()

	keys := tokenverifier.NewJWKS(tokenverifier.JWKSConfig{URL: server.URL})

	got, err := keys.KeyByID(context.Background(), "kid-1")
	if err != nil {
		t.Fatalf("KeyByID: %v", err)
	}
	if got.N.Cmp(key.PublicKey.N) != 0 || got.E != key.PublicKey.E {
		t.Error("the fetched key is not the one the issuer published")
	}
}

// The JWKS endpoint is on the token path, so fetching it per request would put
// an HTTP round trip in front of every authorization call.
func TestTheKeySetIsCachedRatherThanFetchedPerToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	var fetches atomic.Int64
	server := countingJWKSServer(t, &fetches, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	defer server.Close()

	keys := tokenverifier.NewJWKS(tokenverifier.JWKSConfig{URL: server.URL, TTL: time.Hour})
	for range 5 {
		if _, err := keys.KeyByID(context.Background(), "kid-1"); err != nil {
			t.Fatalf("KeyByID: %v", err)
		}
	}

	if got := fetches.Load(); got != 1 {
		t.Errorf("the key set was fetched %d times, want 1", got)
	}
}

// Keycloak rotates signing keys. A kid the cache has never seen is the signal
// that a rotation happened, so it forces exactly one refetch - and a second
// unknown kid must not be able to hammer the issuer.
func TestAnUnknownKeyIDRefetchesTheKeySetOnceAndThenFails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	var fetches atomic.Int64
	server := countingJWKSServer(t, &fetches, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	defer server.Close()

	keys := tokenverifier.NewJWKS(tokenverifier.JWKSConfig{
		URL:            server.URL,
		TTL:            time.Hour,
		MinRefetchWait: time.Hour,
	})
	if _, err := keys.KeyByID(context.Background(), "kid-1"); err != nil {
		t.Fatalf("KeyByID: %v", err)
	}
	if _, err := keys.KeyByID(context.Background(), "rotated-in"); err == nil {
		t.Fatal("KeyByID for an unpublished kid returned no error")
	}
	if _, err := keys.KeyByID(context.Background(), "rotated-in-again"); err == nil {
		t.Fatal("KeyByID for an unpublished kid returned no error")
	}

	if got := fetches.Load(); got != 2 {
		t.Errorf("the key set was fetched %d times, want 2: one warm-up and one rotation refetch", got)
	}
}

func TestATokenSignedByARotatedInKeyVerifiesOnceTheIssuerPublishesIt(t *testing.T) {
	first, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	published := map[string]*rsa.PublicKey{"kid-1": &first.PublicKey}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJWKS(t, w, published)
	}))
	defer server.Close()

	keys := tokenverifier.NewJWKS(tokenverifier.JWKSConfig{URL: server.URL, TTL: time.Hour})
	if _, err := keys.KeyByID(context.Background(), "kid-1"); err != nil {
		t.Fatalf("KeyByID: %v", err)
	}

	published["kid-2"] = &second.PublicKey
	if _, err := keys.KeyByID(context.Background(), "kid-2"); err != nil {
		t.Fatalf("KeyByID after rotation: %v", err)
	}
}

func jwksServer(t *testing.T, keys map[string]*rsa.PublicKey) *httptest.Server {
	t.Helper()
	var ignored atomic.Int64
	return countingJWKSServer(t, &ignored, keys)
}

func countingJWKSServer(t *testing.T, fetches *atomic.Int64, keys map[string]*rsa.PublicKey) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		writeJWKS(t, w, keys)
	}))
}

func writeJWKS(t *testing.T, w http.ResponseWriter, keys map[string]*rsa.PublicKey) {
	t.Helper()
	type jwk struct {
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	set := struct {
		Keys []jwk `json:"keys"`
	}{}
	for kid, key := range keys {
		set.Keys = append(set.Keys, jwk{
			Kty: "RSA",
			Alg: "RS256",
			Use: "sig",
			Kid: kid,
			N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(set); err != nil {
		t.Errorf("encoding the key set: %v", err)
	}
}
