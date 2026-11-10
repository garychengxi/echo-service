// Package cache defines the replaceable cache port. Values must never be authoritative.
package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrMiss = errors.New("cache miss")

// Store has Redis-compatible byte value and TTL semantics. Implementations must
// copy values and be concurrency-safe. No password hashes or tokens belong here.
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, ...string) error
	Close() error
}
type entry struct {
	value   []byte
	expires time.Time
}
type Memory struct {
	mu         sync.RWMutex
	entries    map[string]entry
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
	maxEntries int
}

func NewMemory(cleanup time.Duration) *Memory {
	if cleanup <= 0 {
		cleanup = time.Minute
	}
	m := &Memory{entries: make(map[string]entry), stop: make(chan struct{}), done: make(chan struct{}), maxEntries: 10000}
	go func() {
		defer close(m.done)
		t := time.NewTicker(cleanup)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case now := <-t.C:
				m.mu.Lock()
				for k, v := range m.entries {
					if !v.expires.After(now) {
						delete(m.entries, k)
					}
				}
				m.mu.Unlock()
			}
		}
	}()
	return m
}
func (m *Memory) Get(ctx context.Context, k string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	v, ok := m.entries[k]
	m.mu.RUnlock()
	if !ok || !v.expires.After(time.Now()) {
		return nil, ErrMiss
	}
	return append([]byte(nil), v.value...), nil
}
func (m *Memory) Set(ctx context.Context, k string, v []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		return errors.New("cache TTL must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) >= m.maxEntries {
		for key := range m.entries {
			delete(m.entries, key)
			break
		}
	}
	m.entries[k] = entry{append([]byte(nil), v...), time.Now().Add(ttl)}
	return nil
}
func (m *Memory) Delete(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.entries, k)
	}
	return nil
}
func (m *Memory) Close() error {
	m.once.Do(func() { close(m.stop) })
	<-m.done
	m.mu.Lock()
	clear(m.entries)
	m.mu.Unlock()
	return nil
}
