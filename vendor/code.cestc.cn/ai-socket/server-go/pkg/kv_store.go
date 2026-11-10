package core

import (
	"sync"
	"time"
)

type KVStore interface {
	Get(key string) interface{}
	Set(key string, value interface{})
	Exist(key string) bool
	Delete(key string)
	Count() int
}

type item struct {
	data     interface{}
	expireAt time.Time
}

func (i *item) expired() bool {
	return i.expireAt.Before(time.Now())
}

type InMemoryTTLStore struct {
	store map[string]*item
	ttl   time.Duration
	mutex sync.Mutex
}

func NewInMemoryKVStore(ttl time.Duration) KVStore {
	store := &InMemoryTTLStore{
		store: make(map[string]*item),
		ttl:   ttl,
	}
	go store.startExpirationProcessing()
	return store
}

func (s *InMemoryTTLStore) Get(key string) interface{} {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	i, exists := s.store[key]
	if !exists || i.expired() {
		return nil
	}
	i.expireAt = time.Now().Add(s.ttl)
	return i.data
}

func (s *InMemoryTTLStore) Set(key string, value interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	i := &item{
		data:     value,
		expireAt: time.Now().Add(s.ttl),
	}
	s.store[key] = i
}

func (s *InMemoryTTLStore) Exist(key string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	i, exists := s.store[key]
	if !exists || i.expired() {
		return false
	}
	return true
}

func (s *InMemoryTTLStore) Delete(key string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.store, key)
}

func (s *InMemoryTTLStore) Count() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.store)
}

func (s *InMemoryTTLStore) startExpirationProcessing() {
	timer := time.NewTimer(s.ttl)
	for {
		timer.Reset(s.ttl)
		select {
		case <-timer.C:
			timer.Stop()
			s.mutex.Lock()
			for key, i := range s.store {
				if i.expired() {
					delete(s.store, key)
				}
			}
			s.mutex.Unlock()
		}
	}
}
