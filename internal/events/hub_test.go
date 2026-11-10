package events

import "testing"

func TestIsolationSlowConsumerAndClose(t *testing.T) {
	h := New()
	k := Key{"user", "container"}
	slow := h.Subscribe(k)
	fast := h.Subscribe(k)
	other := h.Subscribe(Key{"other", "container"})
	for i := 0; i < 20; i++ {
		h.Publish(k, Event{Type: "test", Payload: i})
		<-fast.Events
	}
	select {
	case <-slow.Done:
	default:
		t.Fatal("slow client not disconnected")
	}
	select {
	case <-fast.Done:
		t.Fatal("fast client disconnected")
	default:
	}
	select {
	case <-other.Events:
		t.Fatal("cross-user event leaked")
	default:
	}
	h.Invalidate("user", "")
	select {
	case <-fast.Done:
	default:
		t.Fatal("revocation failed")
	}
	h.Close()
	s := h.Subscribe(k)
	h.Unsubscribe(k, s) // Must remain safe after closure.
}
