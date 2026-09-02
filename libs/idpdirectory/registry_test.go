package idpdirectory_test

import (
	"sync"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
)

type stubDirectory struct{ idpdirectory.IdentityDirectory }

func TestRegistryGetReturnsWhatWasRegistered(t *testing.T) {
	registry := idpdirectory.NewRegistry()
	directory := stubDirectory{}
	registry.Register("tenant-a", directory)

	got, ok := registry.Get("tenant-a")
	if !ok || got != idpdirectory.IdentityDirectory(directory) {
		t.Errorf("Get(tenant-a) = %v, %v, want the registered directory", got, ok)
	}
}

func TestRegistryGetReportsFalseForAnUnknownTenant(t *testing.T) {
	registry := idpdirectory.NewRegistry()
	if _, ok := registry.Get("tenant-z"); ok {
		t.Error("Get reported a tenant that was never registered")
	}
}

// issue #86: a tenant onboarded at runtime becomes usable with no
// restart - Register must be safe to call while another goroutine is
// reading.
func TestRegistryIsSafeForConcurrentRegisterAndGet(t *testing.T) {
	registry := idpdirectory.NewRegistry()
	registry.Register("tenant-a", stubDirectory{})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			registry.Register("tenant-b", stubDirectory{})
		}()
		go func() {
			defer wg.Done()
			registry.Get("tenant-a")
		}()
	}
	wg.Wait()

	if _, ok := registry.Get("tenant-b"); !ok {
		t.Error("tenant-b was never observed as registered")
	}
}

func TestRegistryKnownListsEveryRegisteredTenant(t *testing.T) {
	registry := idpdirectory.NewRegistry()
	registry.Register("tenant-a", stubDirectory{})
	registry.Register("tenant-b", stubDirectory{})

	known := registry.Known()
	if len(known) != 2 {
		t.Fatalf("Known() = %v, want 2 tenants", known)
	}
}
