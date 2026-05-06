package main

import (
	"sync"
	"time"
)

type ttlCacheItem[T any] struct {
	value     T
	expiresAt time.Time
}

type keyedTTLCache[T any] struct {
	mu    sync.RWMutex
	items map[string]ttlCacheItem[T]
}

func (c *keyedTTLCache[T]) get(key string) (T, bool) {
	var zero T
	now := time.Now()

	c.mu.RLock()
	if c.items == nil {
		c.mu.RUnlock()
		return zero, false
	}
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || now.After(item.expiresAt) {
		if ok {
			c.delete(key)
		}
		return zero, false
	}
	return item.value, true
}

func (c *keyedTTLCache[T]) set(key string, value T, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[string]ttlCacheItem[T])
	}
	c.items[key] = ttlCacheItem[T]{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()
}

func (c *keyedTTLCache[T]) delete(key string) {
	c.mu.Lock()
	if c.items != nil {
		delete(c.items, key)
	}
	c.mu.Unlock()
}

func (c *keyedTTLCache[T]) clear() {
	c.mu.Lock()
	c.items = nil
	c.mu.Unlock()
}

type singleTTLCache[T any] struct {
	mu        sync.RWMutex
	value     T
	expiresAt time.Time
	ok        bool
}

func (c *singleTTLCache[T]) get() (T, bool) {
	var zero T
	now := time.Now()

	c.mu.RLock()
	value, expiresAt, ok := c.value, c.expiresAt, c.ok
	c.mu.RUnlock()
	if !ok || now.After(expiresAt) {
		if ok {
			c.clear()
		}
		return zero, false
	}
	return value, true
}

func (c *singleTTLCache[T]) set(value T, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.value = value
	c.expiresAt = time.Now().Add(ttl)
	c.ok = true
	c.mu.Unlock()
}

func (c *singleTTLCache[T]) clear() {
	c.mu.Lock()
	var zero T
	c.value = zero
	c.expiresAt = time.Time{}
	c.ok = false
	c.mu.Unlock()
}
