// Package control owns authenticated users, container directory and assignments.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"echo-service/internal/agentconn"
	"echo-service/internal/auth"
	"echo-service/internal/cache"
	"echo-service/internal/config"
	"echo-service/internal/directory"
	"echo-service/internal/events"
	"echo-service/internal/storage"
	"echo-service/internal/table"
	sdk "echo-service/sdk/go/echoservicev1"
	"gorm.io/gorm"
)

type Service struct {
	Repo             *storage.SQL
	Directory        *directory.Service
	Auth             *auth.Service
	Connections      *agentconn.Manager
	cache            cache.Store
	limiter          *limiter
	hashSlots        chan struct{}
	connectionConfig config.AgentConnection
	allowedOrigins   string
	// Guards permission changes versus container commands. Revocation waits for
	// bounded reads before closing the associated upstream connection.
	access    sync.RWMutex
	commands  [64]sync.Mutex
	runtimeMu sync.Mutex
	attached  map[*agentconn.Connection]bool
	loaded    map[string]*agentconn.Connection
	approvals map[string]*approval
	tickets   map[string]ticket
	hub       *events.Hub
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(db *gorm.DB, c *config.Config) (*Service, error) {
	if err := storage.Migrate(db); err != nil {
		return nil, err
	}
	if err := storage.MigrateRuntime(db); err != nil {
		return nil, err
	}
	if err := storage.MigrateOperations(db); err != nil {
		return nil, err
	}
	if err := db.Model(&storage.Turn{}).Where("state IN ?", []string{"running", "awaiting_approval", "cancelling"}).Updates(map[string]any{"state": "unknown", "finished_at": time.Now().UnixMilli()}).Error; err != nil {
		return nil, err
	}
	memory := cache.NewMemory(c.Cache.CleanupInterval)
	repo := &storage.SQL{DB: db}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{Repo: repo, Directory: directory.New(repo, memory, c.Cache.TTL), Auth: auth.New(db, c.Auth), Connections: agentconn.NewManager(c.AgentConnection), connectionConfig: c.AgentConnection, allowedOrigins: c.EventStream.AllowedOrigins, cache: memory, limiter: &limiter{entries: make(map[string]limitEntry)}, hashSlots: make(chan struct{}, 4), attached: make(map[*agentconn.Connection]bool), loaded: make(map[string]*agentconn.Connection), approvals: make(map[string]*approval), tickets: make(map[string]ticket), hub: events.New(c.AgentConnection.MaxMessageBytes, c.EventStream.SubscriberQueueSize), ctx: ctx, cancel: cancel}, nil
}
func (s *Service) ConnectionsConfig() config.AgentConnection { return s.connectionConfig }
func (s *Service) invalidate(user, container string) {
	s.hub.Invalidate(user, container)
	s.Connections.Invalidate(user, container)
}
func (s *Service) Close() error {
	s.cancel()
	s.hub.Close()
	_ = s.Connections.Close()
	s.wg.Wait()
	return s.cache.Close()
}
func (s *Service) ContainerFor(ctx context.Context, p auth.Principal, id string) (table.Container, table.UserContainer, error) {
	if !s.Auth.Active(ctx, p) {
		return table.Container{}, table.UserContainer{}, storage.ErrNotFound
	}
	// Authorization always comes from SQL, even while cached metadata is fresh.
	a, e := s.Repo.Assignment(ctx, p.UserID, id)
	if e != nil {
		return table.Container{}, a, e
	}
	fresh, e := s.Repo.Container(ctx, id)
	if e != nil {
		return fresh, a, e
	}
	if fresh.Status != "enabled" {
		return fresh, a, storage.ErrNotFound
	}
	return fresh, a, nil
}
func userDTO(u table.User) sdk.User {
	return sdk.User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, PlatformRole: u.PlatformRole, Status: u.Status}
}
func containerDTO(c table.Container, admin bool) sdk.Container {
	roots := []string{}
	_ = json.Unmarshal([]byte(c.WorkspaceRootsJSON), &roots)
	out := sdk.Container{ExecutionType: "remote", WebURL: c.WebURL, ID: c.ID, Name: c.Name, Status: c.Status, WorkspaceRoots: roots}
	if admin {
		out.Endpoint = c.Endpoint
	}
	return out
}
func assignmentDTO(a table.UserContainer) sdk.Assignment {
	return sdk.Assignment{ID: a.ID, UserID: a.UserID, ContainerID: a.ContainerID, Role: a.Role, Status: a.Status}
}

type limitEntry struct {
	count int
	reset time.Time
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	item := l.entries[key]
	if now.After(item.reset) {
		item = limitEntry{reset: now.Add(time.Minute)}
	}
	if item.count >= 10 {
		return false
	}
	if len(l.entries) >= 10000 {
		for k, v := range l.entries {
			if now.After(v.reset) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= 10000 {
			return false
		}
	}
	item.count++
	l.entries[key] = item
	return true
}
func (s *Service) acquireHash(ctx context.Context) error {
	select {
	case s.hashSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("password workers busy")
	}
}
