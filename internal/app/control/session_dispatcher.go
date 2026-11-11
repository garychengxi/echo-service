package control

import "sync"

// sessionDispatcher is an in-memory actor router: one ordered worker per
// Session, with different Sessions processed independently.
type sessionDispatcher[T any] struct {
	mu      sync.Mutex
	workers map[string]chan T
	handle  func(T)
	queue   int
	wg      sync.WaitGroup
	closed  bool
}

func newSessionDispatcher[T any](queue int, handle func(T)) *sessionDispatcher[T] {
	return &sessionDispatcher[T]{workers: make(map[string]chan T), handle: handle, queue: queue}
}

func (d *sessionDispatcher[T]) Dispatch(sessionID string, item T) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	worker := d.workers[sessionID]
	if worker == nil {
		worker = make(chan T, d.queue)
		d.workers[sessionID] = worker
		d.wg.Add(1)
		go func(queue <-chan T) {
			defer d.wg.Done()
			for event := range queue {
				d.handle(event)
			}
		}(worker)
	}
	d.mu.Unlock()
	worker <- item
	return true
}

func (d *sessionDispatcher[T]) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	for _, worker := range d.workers {
		close(worker)
	}
	d.mu.Unlock()
	d.wg.Wait()
}
