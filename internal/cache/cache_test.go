package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCopiesTTLAndDelete(t *testing.T) {
	m := NewMemory(time.Millisecond)
	defer m.Close()
	ctx := context.Background()
	input := []byte("old")
	if e := m.Set(ctx, "key", input, 15*time.Millisecond); e != nil {
		t.Fatal(e)
	}
	input[0] = 'X'
	v, e := m.Get(ctx, "key")
	if e != nil || string(v) != "old" {
		t.Fatal("mutable input leaked")
	}
	v[0] = 'Y'
	v, _ = m.Get(ctx, "key")
	if string(v) != "old" {
		t.Fatal("mutable output leaked")
	}
	time.Sleep(20 * time.Millisecond)
	if _, e = m.Get(ctx, "key"); !errors.Is(e, ErrMiss) {
		t.Fatal("expired value returned")
	}
	_ = m.Set(ctx, "key", input, time.Minute)
	_ = m.Delete(ctx, "key")
	if _, e = m.Get(ctx, "key"); !errors.Is(e, ErrMiss) {
		t.Fatal("delete failed")
	}
}
