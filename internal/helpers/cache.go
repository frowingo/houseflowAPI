package helpers

import "sync"

type InMemoryCache[T any] struct {
	mu    sync.RWMutex
	store map[string]T
}

func NewInMemoryCache[T any]() *InMemoryCache[T] {
	return &InMemoryCache[T]{
		store: make(map[string]T),
	}
}

func (c *InMemoryCache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.store[key]
	return val, ok
}

func (c *InMemoryCache[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = value
}

func (c *InMemoryCache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, key)
}
