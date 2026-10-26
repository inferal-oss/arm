package meshsshrelay

import (
	"context"
	"sync"
)

// abortWorker owns a context cancellation callback until it has either been
// stopped before execution or has returned. A successful Stop alone is not a
// join when cancellation already started the callback.
type abortWorker struct {
	stop    func() bool
	done    chan struct{}
	once    sync.Once
	stopped bool
}

func startAbortWorker(ctx context.Context, abort func()) *abortWorker {
	w := &abortWorker{done: make(chan struct{})}
	w.stop = context.AfterFunc(ctx, func() {
		defer close(w.done)
		abort()
	})
	return w
}

// StopAndJoin reports whether cancellation was stopped before the callback
// began. It always waits for a callback that was already running.
func (w *abortWorker) StopAndJoin() bool {
	w.once.Do(func() {
		w.stopped = w.stop()
		if w.stopped {
			close(w.done)
		}
		<-w.done
	})
	return w.stopped
}
