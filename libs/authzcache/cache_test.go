package authzcache_test

import (
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/authzcache"
)

func TestGetMissesOnAnAbsentKey(t *testing.T) {
	c := authzcache.New[string, int](10, time.Minute)
	if _, ok := c.Get("missing"); ok {
		t.Error("expected a miss for a key that was never set")
	}
}

func TestSetThenGetReturnsTheStoredValue(t *testing.T) {
	c := authzcache.New[string, int](10, time.Minute)
	c.Set("a", 1)
	got, ok := c.Get("a")
	if !ok || got != 1 {
		t.Errorf("got (%v, %v), want (1, true)", got, ok)
	}
}

func TestGetIsAMissAfterTheTTLElapses(t *testing.T) {
	now := time.Now()
	c := authzcache.NewWithClock[string, int](10, time.Minute, func() time.Time { return now })
	c.Set("a", 1)

	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Error("expected a miss once the entry's TTL has elapsed")
	}
}

// TestSizeNeverExceedsMaxEntriesUnderHighCardinality is the issue #11
// acceptance criterion "Caches are bounded and cannot grow without limit
// under high user cardinality": inserting far more distinct keys than the
// configured bound - simulating many distinct users, tenants or roles -
// must never grow the cache past that bound, with or without a TTL.
func TestSizeNeverExceedsMaxEntriesUnderHighCardinality(t *testing.T) {
	const maxEntries = 100
	c := authzcache.New[int, int](maxEntries, time.Hour)

	for i := 0; i < 100_000; i++ {
		c.Set(i, i)
		if got := c.Len(); got > maxEntries {
			t.Fatalf("after inserting key %d, cache holds %d entries, want at most %d", i, got, maxEntries)
		}
	}
	if got := c.Len(); got != maxEntries {
		t.Errorf("expected the cache to settle at exactly %d entries, got %d", maxEntries, got)
	}
}

// TestLeastRecentlyUsedEntryIsEvictedFirst proves the eviction the bound
// relies on is not just "some entry, any entry" but genuinely
// least-recently-used: a key kept warm by repeated Get calls survives
// while a cold one is evicted first.
func TestLeastRecentlyUsedEntryIsEvictedFirst(t *testing.T) {
	c := authzcache.New[string, int](2, time.Hour)
	c.Set("cold", 1)
	c.Set("warm", 2)

	// Touch "warm" so "cold" becomes the least-recently-used entry.
	c.Get("warm")

	c.Set("new", 3)

	if _, ok := c.Get("cold"); ok {
		t.Error("expected the least-recently-used entry to have been evicted")
	}
	if _, ok := c.Get("warm"); !ok {
		t.Error("expected the recently touched entry to survive eviction")
	}
	if _, ok := c.Get("new"); !ok {
		t.Error("expected the newly inserted entry to be present")
	}
}
