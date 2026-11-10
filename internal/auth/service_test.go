package auth

import (
	"context"
	"echo-service/internal/client"
	"echo-service/internal/config"
	"echo-service/internal/storage"
	"errors"
	"sync"
	"testing"
	"time"
)

func testAuth(t *testing.T) *Service {
	t.Helper()
	c, e := client.Open(config.Database{Path: t.TempDir() + "/db.sqlite", BusyTimeout: time.Second, MaxOpenConns: 1})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	if e = storage.Migrate(c.Database); e != nil {
		t.Fatal(e)
	}
	s := New(c.Database, config.Auth{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour})
	if _, e = s.BootstrapAdmin(context.Background(), "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestLoginRefreshReplayAndLogout(t *testing.T) {
	s := testAuth(t)
	ctx := context.Background()
	if _, _, e := s.Login(ctx, "admin", "wrong", "test"); !errors.Is(e, ErrCredentials) {
		t.Fatal(e)
	}
	tokens, u, e := s.Login(ctx, "ADMIN", "a-long-test-password", "test")
	if e != nil {
		t.Fatal(e)
	}
	if u.PasswordHash != "" {
		t.Fatal("password hash leaked")
	}
	p, e := s.Authenticate(ctx, tokens.AccessToken)
	if e != nil || p.UserID != u.ID {
		t.Fatal(e)
	}
	if !s.Active(ctx, p) {
		t.Fatal("fresh token not active")
	}
	next, _, e := s.Refresh(ctx, tokens.RefreshToken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, tokens.AccessToken); !errors.Is(e, ErrToken) {
		t.Fatal("old access token still valid")
	}
	if s.Active(ctx, p) {
		t.Fatal("rotated token still active for stream authorization")
	}
	if _, _, e = s.Refresh(ctx, tokens.RefreshToken); !errors.Is(e, ErrReplay) {
		t.Fatal("replay not detected", e)
	}
	if _, e = s.Authenticate(ctx, next.AccessToken); !errors.Is(e, ErrToken) {
		t.Fatal("refresh family not revoked")
	}
	tokens, _, e = s.Login(ctx, "admin", "a-long-test-password", "test")
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Authenticate(ctx, tokens.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, p, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, tokens.AccessToken); !errors.Is(e, ErrToken) {
		t.Fatal("logout did not revoke")
	}
}
func TestConcurrentRefreshFailsClosed(t *testing.T) {
	s := testAuth(t)
	ctx := context.Background()
	tokens, _, e := s.Login(ctx, "admin", "a-long-test-password", "test")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, e := s.Refresh(ctx, tokens.RefreshToken); results <- e }()
	}
	wg.Wait()
	close(results)
	success, replay := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrReplay) {
			replay++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || replay != 1 {
		t.Fatal(success, replay)
	}
}
func TestPasswordAndDisableRevoke(t *testing.T) {
	s := testAuth(t)
	ctx := context.Background()
	tokens, u, e := s.Login(ctx, "admin", "a-long-test-password", "test")
	if e != nil {
		t.Fatal(e)
	}
	p, _ := s.Authenticate(ctx, tokens.AccessToken)
	if e = s.ChangePassword(ctx, p, "a-long-test-password", "another-long-password"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, tokens.AccessToken); !errors.Is(e, ErrToken) {
		t.Fatal("password update did not revoke")
	}
	if s.db.Model(&u).Update("status", "disabled").Error != nil {
		t.Fatal("disable")
	}
	if _, _, e = s.Login(ctx, "admin", "another-long-password", "test"); !errors.Is(e, ErrCredentials) {
		t.Fatal("disabled login allowed")
	}
}
