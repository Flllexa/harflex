package application

import "sync"

// keyedMutex is a mutex per key (a session, a pipeline): work on one key never waits for another. The zero value is
// ready; keys are never forgotten, and there are only as many as sessions, projects and pipelines.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*sync.Mutex{}
	}
	lock := k.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		k.locks[key] = lock
	}
	k.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// keyedRWMutex is a read-write mutex per key (a project).
type keyedRWMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.RWMutex
}

func (k *keyedRWMutex) get(key string) *sync.RWMutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.locks == nil {
		k.locks = map[string]*sync.RWMutex{}
	}
	lock := k.locks[key]
	if lock == nil {
		lock = &sync.RWMutex{}
		k.locks[key] = lock
	}
	return lock
}

func (k *keyedRWMutex) rlock(key string) (unlock func()) {
	lock := k.get(key)
	lock.RLock()
	return lock.RUnlock
}

func (k *keyedRWMutex) lock(key string) (unlock func()) {
	lock := k.get(key)
	lock.Lock()
	return lock.Unlock
}
