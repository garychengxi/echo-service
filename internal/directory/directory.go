// Package directory decorates the repository with a replaceable read-through cache.
package directory

import (
	"context"
	"echo-service/internal/cache"
	"echo-service/internal/storage"
	"echo-service/internal/table"
	"encoding/json"
	"sync"
	"time"
)

type Service struct {
	repo   storage.Directory
	cache  cache.Store
	ttl    time.Duration
	mu     sync.Mutex
	bypass bool
}

func New(repo storage.Directory, c cache.Store, ttl time.Duration) *Service {
	return &Service{repo: repo, cache: c, ttl: ttl}
}

// Serialize fills and mutations so an old in-flight DB read cannot repopulate a
// deleted key. Multi-instance Redis needs distributed versioning/invalidation.
func (s *Service) User(ctx context.Context, id string) (table.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "directory:v1:user:" + id
	if b, e := s.get(ctx, key); e == nil {
		var u table.User
		if json.Unmarshal(b, &u) == nil {
			return u, nil
		}
	}
	u, e := s.repo.User(ctx, id)
	if e != nil {
		return u, e
	}
	u.PasswordHash = ""
	s.fill(ctx, key, u)
	return u, nil
}
func (s *Service) Container(ctx context.Context, id string) (table.Container, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "directory:v1:container:" + id
	if b, e := s.get(ctx, key); e == nil {
		var c table.Container
		if json.Unmarshal(b, &c) == nil {
			return c, nil
		}
	}
	c, e := s.repo.Container(ctx, id)
	if e != nil {
		return c, e
	}
	s.fill(ctx, key, c)
	return c, nil
}
func (s *Service) UpdateUser(ctx context.Context, id string, values map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.repo.UpdateUser(ctx, id, values); e != nil {
		return e
	}
	s.invalidate("directory:v1:user:" + id)
	return nil
}
func (s *Service) UpdateContainer(ctx context.Context, id string, values map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.repo.UpdateContainer(ctx, id, values); e != nil {
		return e
	}
	s.invalidate("directory:v1:container:" + id)
	return nil
}
func (s *Service) fill(ctx context.Context, key string, v any) {
	if s.bypass {
		return
	}
	b, e := json.Marshal(v)
	if e == nil {
		_ = s.cache.Set(ctx, key, b, s.ttl)
	}
}
func (s *Service) invalidate(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if s.cache.Delete(ctx, key) != nil {
		s.bypass = true
	}
}

func (s *Service) get(ctx context.Context, key string) ([]byte, error) {
	if s.bypass {
		return nil, cache.ErrMiss
	}
	return s.cache.Get(ctx, key)
}
