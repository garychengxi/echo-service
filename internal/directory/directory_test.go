package directory

import (
	"context"
	"errors"
	"testing"
	"time"

	"echo-service/internal/cache"
	"echo-service/internal/storage"
	"echo-service/internal/table"
)

type repoStub struct {
	storage.Directory
	name  string
	reads int
}

func (r *repoStub) User(context.Context, string) (table.User, error) {
	r.reads++
	return table.User{ID: "one", DisplayName: r.name, PasswordHash: "secret-hash"}, nil
}
func (r *repoStub) UpdateUser(_ context.Context, _ string, v map[string]any) error {
	r.name = v["display_name"].(string)
	return nil
}

type brokenDelete struct{ cache.Store }

func (brokenDelete) Delete(context.Context, ...string) error { return errors.New("cache unavailable") }

func TestReadThroughStripsSecretsAndBypassesFailedInvalidation(t *testing.T) {
	m := cache.NewMemory(time.Minute)
	defer m.Close()
	r := &repoStub{name: "before"}
	s := New(r, brokenDelete{m}, time.Minute)
	ctx := context.Background()
	u, err := s.User(ctx, "one")
	if err != nil || u.PasswordHash != "" {
		t.Fatal("hash cached", err)
	}
	_, _ = s.User(ctx, "one")
	if r.reads != 1 {
		t.Fatal("cache did not hit")
	}
	if err := s.UpdateUser(ctx, "one", map[string]any{"display_name": "after"}); err != nil {
		t.Fatal(err)
	}
	u, err = s.User(ctx, "one")
	if err != nil || u.DisplayName != "after" {
		t.Fatal("stale value served after invalidation failure", err)
	}
}
