package modelcatalog

import (
	"sync"
	"time"
)

const CacheCapacity = 64

// CredentialIdentity is an internal credential fingerprint, never a key value or UI field.
type CacheKey struct {
	BackendID          string
	Revision           string
	Origin             string
	CredentialIdentity string
	SearchTerm         string
}

type cacheEntry struct {
	value Result
	at    time.Time
	touch uint64
}

type Cache struct {
	mu       sync.Mutex
	now      func() time.Time
	values   map[CacheKey]cacheEntry
	sequence uint64
}

func NewCache(now func() time.Time) *Cache {
	return &Cache{now: now, values: make(map[CacheKey]cacheEntry)}
}

func cloneResult(value Result) Result {
	if value.Models != nil {
		value.Models = append([]Model{}, value.Models...)
	}
	for i := range value.Models {
		item := &value.Models[i]
		if item.SupportedReasoningEfforts != nil {
			item.SupportedReasoningEfforts = append([]string{}, item.SupportedReasoningEfforts...)
		}
		if item.Loaded != nil {
			loaded := *item.Loaded
			item.Loaded = &loaded
		}
	}
	return value
}

func (c *Cache) Get(key CacheKey) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.values[key]
	if !ok {
		return Result{}, false
	}
	if c.now().Sub(entry.at) >= 5*time.Minute {
		delete(c.values, key)
		return Result{}, false
	}
	c.sequence++
	entry.touch = c.sequence
	c.values[key] = entry
	return cloneResult(entry.value), true
}

func (c *Cache) Put(key CacheKey, value Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	for candidate, entry := range c.values {
		if now.Sub(entry.at) >= 5*time.Minute {
			delete(c.values, candidate)
		}
	}
	if !value.Complete || (value.Status != StatusComplete && value.Status != StatusEmpty) {
		return
	}
	c.sequence++
	c.values[key] = cacheEntry{value: cloneResult(value), at: now, touch: c.sequence}
	if len(c.values) > CacheCapacity {
		oldest := ^uint64(0)
		var victim CacheKey
		for candidate, entry := range c.values {
			if entry.touch < oldest {
				oldest, victim = entry.touch, candidate
			}
		}
		delete(c.values, victim)
	}
}
