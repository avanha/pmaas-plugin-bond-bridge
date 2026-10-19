package bondbridge

import (
	"context"
	"sync"
	"time"
)

// executor makes the calls to the bridge, one at a time and off the plugin's goroutine: the bridge is a small device that doesn't
// like being asked many things at once, and a call can take seconds. Urgent jobs (the stop that ends a dimming) go ahead of the
// others. It also asks for the state of everything every poll interval.
type executor struct {
	urgent chan func(ctx context.Context)
	normal chan func(ctx context.Context)
	poll   func(ctx context.Context)
	every  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

func newExecutor(poll func(ctx context.Context), every time.Duration) *executor {
	ctx, cancel := context.WithCancel(context.Background())

	return &executor{
		urgent: make(chan func(ctx context.Context), 64),
		normal: make(chan func(ctx context.Context), 256),
		poll:   poll,
		every:  every,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (e *executor) start() {
	e.wg.Go(e.run)
}

func (e *executor) run() {
	var tick <-chan time.Time

	if e.every > 0 {
		ticker := time.NewTicker(e.every)
		defer ticker.Stop()

		tick = ticker.C
	}

	for {
		// An urgent job, if there is one, before anything else.
		select {
		case job := <-e.urgent:
			job(e.ctx)

			continue
		default:
		}

		select {
		case <-e.ctx.Done():
			return
		case job := <-e.urgent:
			job(e.ctx)
		case job := <-e.normal:
			job(e.ctx)
		case <-tick:
			e.poll(e.ctx)
		}
	}
}

// submit queues a job, and reports whether it was: not if the executor has stopped, or is so far behind that the bridge is
// plainly not answering.
func (e *executor) submit(job func(ctx context.Context)) bool {
	return e.send(e.normal, job)
}

// submitUrgent queues a job ahead of the others.
func (e *executor) submitUrgent(job func(ctx context.Context)) bool {
	return e.send(e.urgent, job)
}

func (e *executor) send(queue chan func(ctx context.Context), job func(ctx context.Context)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return false
	}

	select {
	case queue <- job:
		return true
	default:
		return false
	}
}

// stop ends it, and waits for the job it is in the middle of.
func (e *executor) stop() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()

	e.cancel()
	e.wg.Wait()
}
