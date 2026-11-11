package control

import (
	"sync"
	"testing"
	"time"
)

func TestSessionDispatcherOrdersPerSessionWithoutCrossSessionBlocking(t *testing.T) {
	type item struct {
		session string
		value   int
	}
	blockA := make(chan struct{})
	bDone := make(chan struct{})
	var mu sync.Mutex
	seen := map[string][]int{}
	d := newSessionDispatcher(4, func(event item) {
		if event.session == "a" && event.value == 1 {
			<-blockA
		}
		mu.Lock()
		seen[event.session] = append(seen[event.session], event.value)
		mu.Unlock()
		if event.session == "b" {
			close(bDone)
		}
	})
	if !d.Dispatch("a", item{session: "a", value: 1}) || !d.Dispatch("a", item{session: "a", value: 2}) || !d.Dispatch("b", item{session: "b", value: 1}) {
		t.Fatal("dispatch rejected")
	}
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("session b was blocked by session a")
	}
	close(blockA)
	d.Close()
	mu.Lock()
	defer mu.Unlock()
	if got := seen["a"]; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("session order lost: %v", got)
	}
}

func TestSessionDispatcherContainsBacklogToOneSession(t *testing.T) {
	type item struct{ session string }
	blockA := make(chan struct{})
	bDone := make(chan struct{})
	d := newSessionDispatcher(1, func(event item) {
		if event.session == "a" {
			<-blockA
		} else {
			close(bDone)
		}
	})
	if !d.Dispatch("a", item{session: "a"}) {
		t.Fatal("first session a event rejected")
	}
	time.Sleep(10 * time.Millisecond)
	if !d.Dispatch("a", item{session: "a"}) {
		t.Fatal("session a queue was not available")
	}
	if d.Dispatch("a", item{session: "a"}) {
		t.Fatal("overflowing session a queue was accepted")
	}
	if !d.Dispatch("b", item{session: "b"}) {
		t.Fatal("session b was affected by session a backlog")
	}
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("session b was blocked by session a backlog")
	}
	close(blockA)
	d.Close()
}
