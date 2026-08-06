// Package authzcache is the bounded, in-process read-through cache shape
// used across the ADS's hot decision path: role permissions, user
// overrides, the capability catalog and IdP metadata (issue #11, §11.2,
// §15.1). It generalizes the pattern the assignment-matrix cache already
// proved out (apps/ads/internal/assignments.CachingRoleMatrix) into one
// reusable, generic type so every cache on the hot path shares the same
// bound rather than each growing its own ad hoc map.
//
// Every entry is a fact resolved from an authoritative source, never a
// verdict, so a stale or evicted entry can only be wrong about data, never
// about precedence (§6.3, ADR-003).
package authzcache

import (
	"container/list"
	"sync"
	"time"
)

// Cache is a bounded, TTL-aged, least-recently-used cache safe for
// concurrent use.
//
// It is bounded on two independent axes, both required under high user
// cardinality (issue #11 acceptance criteria): TTL bounds how long a
// cached fact may outlive a change to its source, and maxEntries bounds
// how much memory the cache can ever hold regardless of how many distinct
// keys - tenants, users, roles, capability revisions - are ever seen.
// Without the second bound, a system with many tenants and users would
// grow the cache forever even though every individual entry keeps
// expiring on time.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	order   *list.List
	entries map[K]*list.Element
}

type cacheEntry[K comparable, V any] struct {
	key    K
	value  V
	readAt time.Time
}

// New builds a Cache bounded to maxEntries, aging entries out after ttl.
// maxEntries <= 0 means unbounded by count (TTL alone still applies);
// ttl <= 0 means entries never expire on their own (size alone still
// applies). Passing neither bound defeats the purpose of this package, but
// nothing here refuses it - a caller reaching for an unbounded cache is a
// design decision to be caught in review, not a runtime panic.
func New[K comparable, V any](maxEntries int, ttl time.Duration) *Cache[K, V] {
	return NewWithClock[K, V](maxEntries, ttl, time.Now)
}

// NewWithClock is New with an injectable clock, so a test can advance time
// without sleeping.
func NewWithClock[K comparable, V any](maxEntries int, ttl time.Duration, now func() time.Time) *Cache[K, V] {
	return &Cache[K, V]{
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
		order:      list.New(),
		entries:    make(map[K]*list.Element),
	}
}

// Get reads a value, treating an expired entry as a miss and evicting it.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}

	entry := elem.Value.(*cacheEntry[K, V])
	if c.ttl > 0 && c.now().Sub(entry.readAt) >= c.ttl {
		c.removeElement(elem)
		var zero V
		return zero, false
	}

	c.order.MoveToFront(elem)
	return entry.value, true
}

// Set stores a value, evicting the least-recently-used entry first if the
// cache is already at capacity and key is not already present.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if elem, ok := c.entries[key]; ok {
		elem.Value.(*cacheEntry[K, V]).value = value
		elem.Value.(*cacheEntry[K, V]).readAt = now
		c.order.MoveToFront(elem)
		return
	}

	if c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		c.evictOldest()
	}

	elem := c.order.PushFront(&cacheEntry[K, V]{key: key, value: value, readAt: now})
	c.entries[key] = elem
}

// Len reports the current number of live entries, expired or not. It exists
// for tests proving the size bound holds; production callers have no need
// for it.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache[K, V]) evictOldest() {
	oldest := c.order.Back()
	if oldest != nil {
		c.removeElement(oldest)
	}
}

func (c *Cache[K, V]) removeElement(elem *list.Element) {
	entry := elem.Value.(*cacheEntry[K, V])
	delete(c.entries, entry.key)
	c.order.Remove(elem)
}
