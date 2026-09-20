package main

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
)

//go:generate moq -out quitter_moq_test.go -pkg main -skip-ensure -fmt goimports . quitter

// quitter is the consumer-side interface for *tea.Program's Quit, so the
// signal-to-quit wiring stays unit-testable with a mock.
type quitter interface{ Quit() }

// shutdownGuard turns a terminating OS signal (SIGHUP/SIGTERM) into a single
// graceful Quit and records that the exit was signal-driven. SIGINT is caught
// and drained without quitting (see watch/handle). The flag is read only after
// p.Run() joins, so the annotation store is never touched off the main goroutine.
type shutdownGuard struct {
	once     sync.Once
	signaled atomic.Bool
}

func (g *shutdownGuard) trigger(quit func()) {
	g.once.Do(func() {
		g.signaled.Store(true)
		quit()
	})
}

func (g *shutdownGuard) handle(ch <-chan os.Signal, quit func()) {
	for sig := range ch {
		if sig == syscall.SIGINT {
			// caught-and-ignored: a Ctrl-C meant for an external $EDITOR must not
			// quit igit. Notify keeps SIGINT from default-terminating the process.
			continue
		}
		g.trigger(quit)
	}
}

func (g *shutdownGuard) watch(q quitter) (stop func()) {
	ch := make(chan os.Signal, 1)
	// SIGHUP and SIGTERM quit gracefully. SIGINT is registered only to take
	// away its terminate disposition and is then drained, so a Ctrl-C meant
	// for an external $EDITOR cannot kill igit. stop() restores the default
	// disposition for all three, so a second signal can end a hung finalize.
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go g.handle(ch, q.Quit)
	// idempotent: run() calls stop() explicitly before finalize and via defer.
	return sync.OnceFunc(func() {
		signal.Stop(ch)
		close(ch)
	})
}

func (g *shutdownGuard) wasSignaled() bool {
	return g.signaled.Load()
}
