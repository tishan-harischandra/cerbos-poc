package tokenverifier

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// DefaultJWKSTTL bounds how long a cached key set may outlive a rotation. Keys
// are fetched off the token path, so this is only the upper bound: an unknown
// kid refetches immediately.
const DefaultJWKSTTL = 15 * time.Minute

// DefaultMinRefetchWait rate-limits rotation refetches. Without it a stream of
// tokens naming a kid the issuer never published turns into a stream of
// requests to the issuer, which is a denial-of-service amplifier pointed at the
// one dependency every login needs.
const DefaultMinRefetchWait = 30 * time.Second

// DefaultJWKSTimeout bounds one fetch, so a hung issuer cannot pin a request
// goroutine indefinitely.
const DefaultJWKSTimeout = 5 * time.Second

// ErrKeyNotPublished means the issuer's key set does not contain the kid.
var ErrKeyNotPublished = errors.New("the issuer does not publish this key id")

// JWKSConfig configures the key source.
type JWKSConfig struct {
	URL    string
	Client *http.Client
	// TTL bounds how long a cached set is served without refetching.
	TTL time.Duration
	// MinRefetchWait is the shortest interval between two rotation-driven
	// refetches.
	MinRefetchWait time.Duration
	Now            func() time.Time
}

// JWKS is a caching key source backed by an issuer's JWKS endpoint.
type JWKS struct {
	cfg JWKSConfig

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	// lastRotationRefetch tracks only the refetches an unknown kid triggered.
	// A scheduled TTL refresh must not start the rate-limit clock, or the
	// first rotation after one would be ignored for a whole interval.
	lastRotationRefetch time.Time
}

// NewJWKS returns a key source that reads the issuer's published keys.
func NewJWKS(cfg JWKSConfig) *JWKS {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: DefaultJWKSTimeout}
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultJWKSTTL
	}
	if cfg.MinRefetchWait <= 0 {
		cfg.MinRefetchWait = DefaultMinRefetchWait
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &JWKS{cfg: cfg}
}

// KeyByID returns the published key with the given id, refetching the set once
// when the id is unknown, which is what a key rotation looks like from here.
func (j *JWKS) KeyByID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := j.cfg.Now()
	if j.keys == nil || now.Sub(j.fetchedAt) >= j.cfg.TTL {
		if err := j.refresh(ctx, now); err != nil {
			return nil, err
		}
	}

	if key, ok := j.keys[kid]; ok {
		return key, nil
	}

	if !j.lastRotationRefetch.IsZero() && now.Sub(j.lastRotationRefetch) < j.cfg.MinRefetchWait {
		return nil, fmt.Errorf("%w: %q", ErrKeyNotPublished, kid)
	}
	j.lastRotationRefetch = now
	if err := j.refresh(ctx, now); err != nil {
		return nil, err
	}
	if key, ok := j.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrKeyNotPublished, kid)
}

func (j *JWKS) refresh(ctx context.Context, now time.Time) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, j.cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("building the key set request: %w", err)
	}
	response, err := j.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("fetching the key set: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching the key set: the issuer answered %s", response.Status)
	}
	// A malicious or misconfigured issuer must not be able to make this
	// service read an unbounded body into memory.
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the key set: %w", err)
	}

	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("decoding the key set: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, entry := range set.Keys {
		// Encryption keys share the endpoint with signing keys. Accepting one
		// as a signature key would widen what counts as a valid token.
		if entry.Kty != "RSA" || (entry.Use != "" && entry.Use != "sig") {
			continue
		}
		key, err := rsaKey(entry.N, entry.E)
		if err != nil {
			continue
		}
		keys[entry.Kid] = key
	}

	j.keys = keys
	j.fetchedAt = now
	return nil
}

func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, fmt.Errorf("decoding the modulus: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, fmt.Errorf("decoding the exponent: %w", err)
	}
	if len(e) == 0 || len(e) > 8 {
		return nil, errors.New("the exponent is not a usable size")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}
