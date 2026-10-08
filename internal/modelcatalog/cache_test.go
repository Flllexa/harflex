package modelcatalog

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCacheSeparatesRevisionOriginPurposeAndSearch(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cache := NewCache(func() time.Time { return now })
	key := CacheKey{
		BackendID: "router", Revision: "r1", Origin: "https://router:443",
		CredentialIdentity: "inference:one|management:one", SearchTerm: "gpt",
	}
	loaded := true
	original := Result{Status: StatusComplete, Complete: true, Models: []Model{{
		ID: "m", Loaded: &loaded, SupportedReasoningEfforts: []string{"low"},
	}}}
	cache.Put(key, original)
	original.Models[0].ID = "mutated"
	original.Models[0].SupportedReasoningEfforts[0] = "mutated"
	loaded = false

	got, ok := cache.Get(key)
	if !ok || got.Models[0].ID != "m" || got.Models[0].SupportedReasoningEfforts[0] != "low" ||
		got.Models[0].Loaded == nil || !*got.Models[0].Loaded {
		t.Fatal(got, ok)
	}
	got.Models[0].ID = "changed on read"
	got.Models[0].SupportedReasoningEfforts[0] = "changed on read"
	*got.Models[0].Loaded = false
	again, ok := cache.Get(key)
	if !ok || again.Models[0].ID != "m" || again.Models[0].SupportedReasoningEfforts[0] != "low" ||
		again.Models[0].Loaded == nil || !*again.Models[0].Loaded {
		t.Fatal(again, ok)
	}

	for _, other := range []CacheKey{
		{BackendID: "other", Revision: key.Revision, Origin: key.Origin, CredentialIdentity: key.CredentialIdentity, SearchTerm: key.SearchTerm},
		{BackendID: key.BackendID, Revision: "r2", Origin: key.Origin, CredentialIdentity: key.CredentialIdentity, SearchTerm: key.SearchTerm},
		{BackendID: key.BackendID, Revision: key.Revision, Origin: "https://other:443", CredentialIdentity: key.CredentialIdentity, SearchTerm: key.SearchTerm},
		{BackendID: key.BackendID, Revision: key.Revision, Origin: key.Origin, CredentialIdentity: "inference:one|management:two", SearchTerm: key.SearchTerm},
		{BackendID: key.BackendID, Revision: key.Revision, Origin: key.Origin, CredentialIdentity: key.CredentialIdentity, SearchTerm: "claude"},
	} {
		if _, ok := cache.Get(other); ok {
			t.Fatalf("wrong cache hit: %+v", other)
		}
	}
	now = now.Add(5 * time.Minute)
	if _, ok := cache.Get(key); ok {
		t.Fatal("read extended the five-minute TTL")
	}
}

func TestCacheRejectsPartialAndFailed(t *testing.T) {
	cache := NewCache(time.Now)
	key := CacheKey{BackendID: "b", Revision: "r", Origin: "o"}
	for _, status := range []Status{StatusPartial, StatusFailed, StatusInterrupted, StatusUnsupported} {
		cache.Put(key, Result{Status: status, Complete: true, Models: []Model{{ID: "x"}}})
		if _, ok := cache.Get(key); ok {
			t.Fatalf("cached %s", status)
		}
	}
	for _, status := range []Status{StatusComplete, StatusEmpty} {
		cache.Put(key, Result{Status: status, Complete: false})
		if _, ok := cache.Get(key); ok {
			t.Fatalf("cached incomplete %s", status)
		}
	}
	for _, status := range []Status{StatusComplete, StatusEmpty} {
		cache.Put(key, Result{Status: status, Complete: true})
		if _, ok := cache.Get(key); !ok {
			t.Fatalf("did not cache complete %s", status)
		}
	}
}

func TestCacheManySearchesHaveDeterministicLRUAndExpiredSweep(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cache := NewCache(func() time.Time { return now })
	key := func(i int) CacheKey {
		return CacheKey{BackendID: "router", Revision: "r", Origin: "https://router:443",
			CredentialIdentity: "inference:one", SearchTerm: fmt.Sprintf("term-%03d", i)}
	}
	result := Result{Status: StatusComplete, Complete: true, Models: []Model{{ID: "m"}}}
	for i := 0; i < CacheCapacity; i++ {
		cache.Put(key(i), result)
	}
	if _, ok := cache.Get(key(0)); !ok {
		t.Fatal("hot entry absent")
	}
	cache.Put(key(CacheCapacity), result)
	if len(cache.values) != CacheCapacity {
		t.Fatalf("cache entries=%d", len(cache.values))
	}
	if _, ok := cache.Get(key(1)); ok {
		t.Fatal("least recently used term was retained")
	}
	if _, ok := cache.Get(key(0)); !ok {
		t.Fatal("recently read term was evicted")
	}
	for i := CacheCapacity + 1; i < 256; i++ {
		cache.Put(key(i), result)
	}
	if len(cache.values) != CacheCapacity {
		t.Fatalf("many searches retained %d entries", len(cache.values))
	}
	if _, ok := cache.Get(key(255)); !ok {
		t.Fatal("newest term was evicted")
	}
	now = now.Add(5 * time.Minute)
	cache.Put(key(256), result)
	if len(cache.values) != 1 {
		t.Fatalf("expired entries not swept: %d", len(cache.values))
	}
	if _, ok := cache.Get(key(255)); ok {
		t.Fatal("expired entry survived")
	}
}

func TestCacheConcurrentGetPut(t *testing.T) {
	cache := NewCache(time.Now)
	value := Result{Status: StatusComplete, Complete: true, Models: []Model{{ID: "m"}}}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			for j := 0; j < 32; j++ {
				key := CacheKey{BackendID: "b", SearchTerm: fmt.Sprintf("%d:%d", i, j)}
				cache.Put(key, value)
				cache.Get(key)
			}
		}(i)
	}
	workers.Wait()
	if len(cache.values) > CacheCapacity {
		t.Fatalf("cache entries=%d", len(cache.values))
	}
}
