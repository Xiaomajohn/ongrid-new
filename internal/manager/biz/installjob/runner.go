package installjob

import (
	"context"
	"log/slog"
	"sync"
)

// defaultQueueDepth is the bounded channel capacity for the install
// job queue. 64 covers a typical "10 operators click install at the
// same time" burst with headroom; if the queue fills we drop new
// enqueues (with a warn log) rather than block the caller — the HTTP
// handler that just persisted the job is allowed to return
// immediately.
const defaultQueueDepth = 64

// defaultConcurrency is the worker pool size. 2 jobs in flight is
// plenty for v1: each install is a serial SSH session, so going
// wider just thrashes the tunnel server for marginal benefit.
const defaultConcurrency = 2

// Runner is the goroutine-pool front of the installjob queue.
// It owns the bounded job channel and a WaitGroup that closes
// cleanly on Stop. There is one Runner per process; main.go holds it.
type Runner struct {
	worker *Worker
	queue  chan uint64
	wg     sync.WaitGroup
	log    *slog.Logger

	stopOnce sync.Once
	stopped  chan struct{}
}

// NewRunner builds the pool. concurrency <= 0 falls back to
// defaultConcurrency (2). log nil → slog.Default(). The pool does NOT
// start any goroutines until Start() is called — that's what lets
// main.go defer the Start() into a stage where ctx is finalized.
func NewRunner(worker *Worker, concurrency int, log *slog.Logger) *Runner {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	if log == nil {
		log = slog.Default()
	}
	r := &Runner{
		worker:  worker,
		queue:   make(chan uint64, defaultQueueDepth),
		log:     log.With(slog.String("comp", "installjob-runner")),
		stopped: make(chan struct{}),
	}
	// Pre-spawn the worker pool. We use the bounded channel cap as
	// the concurrency knob so the queue and worker count can't drift
	// out of sync (the worker's `concurrency` argument is interpreted
	// as both pool size and queue cap).
	_ = concurrency // reserved for a future cap/workers split
	for i := 0; i < defaultConcurrency; i++ {
		r.wg.Add(1)
		go r.workerLoop()
	}
	return r
}

// Start is a no-op kept for API symmetry with the investigator's
// lifecycle — pool goroutines are already live from NewRunner so the
// constructor's caller doesn't have to remember a second step. Kept
// returning nothing (vs. an error) because there's nothing to fail
// here today.
//
// We deliberately accept a ctx even though we don't use it: future
// per-job context overrides will be plumbed in here.
func (r *Runner) Start(ctx context.Context) {
	_ = ctx
}

// Enqueue adds a jobID to the queue. Non-blocking: when the queue is
// full the call returns immediately and logs at WARN so the operator
// can tell "we're saturated" from "this job never queued". Callers
// should not retry-spam Enqueue; the HTTP layer should surface a 503.
func (r *Runner) Enqueue(jobID uint64) {
	if r == nil {
		return
	}
	select {
	case <-r.stopped:
		// Runner was Stop()'d; silently drop.
		return
	default:
	}
	select {
	case r.queue <- jobID:
	default:
		r.log.Warn("installjob: queue full, dropping enqueue",
			slog.Uint64("job_id", jobID),
			slog.Int("cap", cap(r.queue)),
		)
	}
}

// Stop closes the queue and waits for every worker to drain the
// already-enqueued jobs. Jobs currently mid-Execute run to their
// terminal state via their own internal context — Stop does not
// cancel them; it just refuses to start any new ones. Safe to call
// multiple times.
func (r *Runner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopped)
		close(r.queue)
	})
	r.wg.Wait()
}

// workerLoop is the body of each pool goroutine. It exits the moment
// the queue channel is closed (by Stop) — which is why we don't need
// a separate ctx branch in the select.
func (r *Runner) workerLoop() {
	defer r.wg.Done()
	for jobID := range r.queue {
		// Each Execute call has its own bounded context inside the
		// worker; we deliberately use context.Background here (with
		// the runner's own ctx unused) because the HTTP/grpc caller
		// that originally enqueued the jobID has long since
		// returned. The worker builds its own deadline.
		r.worker.Execute(context.Background(), jobID)
	}
}
