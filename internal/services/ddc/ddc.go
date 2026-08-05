// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package ddc controls external monitor brightness over DDC/CI.
//
// The bar has to stay responsive while the bus does not: a DDC write costs a
// mandated ~50ms, and a scroll produces events far faster than that. So the
// rendered value and the wire are decoupled. A Set updates the cached value
// and returns immediately; a per-display worker goroutine owns the transport
// and writes the newest requested value whenever the bus is next free.
package ddc

import (
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/pkg/module"
)

const (
	// probeDeadline bounds the opening handshake. A display that has not
	// answered by now is not going to.
	probeDeadline = 2 * time.Second

	// stopTimeout bounds how long service shutdown waits on a worker that
	// is stuck mid-transaction.
	stopTimeout = 2 * time.Second

	// failMax is how many consecutive errors mark a display unusable. It
	// stops there rather than exiting: a monitor that is merely asleep
	// comes back.
	failMax = 5

	// Poll backoff after failures, so a dead bus costs almost nothing.
	backoffMin = 1 * time.Second
	backoffMax = 60 * time.Second
)

// Event is a complete snapshot of a display's state, never a delta. That is
// what makes replacing an undelivered event on a full channel correct.
type Event struct {
	Connector string
	Cur, Max  uint16
	Pct       int

	// Ready reports whether the display is answering.
	Ready bool

	// Err carries why it is not, when it is not.
	Err error
}

// Service owns one worker per display. Acquire it through services.Acquire
// so a config reload does not tear the workers down and re-probe.
type Service struct {
	mu      sync.Mutex
	workers map[string]*worker
}

// Acquire returns the shared DDC service. It performs no I/O: probing costs
// seconds and belongs on a worker goroutine, not on whoever acquires first.
func Acquire() (*Service, func(), error) {
	return services.Acquire("ddc", func() (*Service, error) {
		return &Service{workers: make(map[string]*worker)}, nil
	})
}

// Watch returns a source of state for d, starting its worker on first use.
// It never blocks: the probe result arrives as the first Event.
func (s *Service) Watch(d Display, poll time.Duration) module.Source[Event] {
	return module.NewSource(func(emit func(Event)) (module.Conn, error) {
		w := s.workerFor(d, poll)
		remove := w.listen(emit)
		return module.ConnFuncs{
			StopFn: func() {
				if w.unlisten(remove) {
					s.drop(d.Connector)
				}
			},
			WakeFn: w.resync,
		}, nil
	})
}

// Set requests a new brightness percentage for a display. Safe to call from
// the module goroutine: it never touches the bus.
func (s *Service) Set(connector string, pct int) {
	s.mu.Lock()
	w := s.workers[connector]
	s.mu.Unlock()
	if w != nil {
		w.set(pct)
	}
}

// Stop is called by services.Acquire once nothing holds the service.
func (s *Service) Stop() error {
	s.mu.Lock()
	ws := make([]*worker, 0, len(s.workers))
	for _, w := range s.workers {
		ws = append(ws, w)
	}
	s.workers = make(map[string]*worker)
	s.mu.Unlock()

	// Signal every worker first, then wait: serial stops would add up to
	// one bus timeout each.
	for _, w := range ws {
		w.signalStop()
	}
	for _, w := range ws {
		select {
		case <-w.done:
		case <-time.After(stopTimeout):
			logging.Log.Warn().Msgf("ddc: %s: worker did not stop in %s", w.d.Connector, stopTimeout)
		}
	}
	return nil
}

func (s *Service) workerFor(d Display, poll time.Duration) *worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.workers[d.Connector]; ok {
		return w
	}
	w := newWorker(d, poll)
	s.workers[d.Connector] = w
	go w.run()
	return w
}

func (s *Service) drop(connector string) {
	s.mu.Lock()
	w := s.workers[connector]
	delete(s.workers, connector)
	s.mu.Unlock()
	if w != nil {
		w.signalStop()
	}
}

// worker owns one display's transport and is the only goroutine that talks
// to it.
type worker struct {
	d    Display
	poll time.Duration

	// mu guards the fields below. It is held only for field copies, never
	// across a syscall or a sleep.
	mu       sync.Mutex
	cur, max uint16
	ready    bool
	lastErr  error
	pending  uint16
	hasPend  bool

	// writeGen increments on every Set. A read that started before a Set
	// is stale by the time it lands, and applying it would snap the bar
	// back to the pre-scroll value; the generation is how that is caught.
	writeGen uint64

	fails int

	wake chan struct{}
	quit chan struct{}
	done chan struct{}

	lmu       sync.Mutex
	listeners map[int]func(Event)
	nextID    int
}

func newWorker(d Display, poll time.Duration) *worker {
	return &worker{
		d:         d,
		poll:      poll,
		wake:      make(chan struct{}, 1),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
		listeners: make(map[int]func(Event)),
	}
}

func (w *worker) listen(fn func(Event)) int {
	w.lmu.Lock()
	defer w.lmu.Unlock()
	id := w.nextID
	w.nextID++
	w.listeners[id] = fn

	// A late subscriber still needs the state the worker already knows.
	w.mu.Lock()
	snap, known := w.snapshotLocked(), w.ready || w.lastErr != nil
	w.mu.Unlock()
	if known {
		go fn(snap)
	}
	return id
}

// unlisten drops a listener and reports whether the worker is now idle.
func (w *worker) unlisten(id int) bool {
	w.lmu.Lock()
	defer w.lmu.Unlock()
	delete(w.listeners, id)
	return len(w.listeners) == 0
}

func (w *worker) emit(ev Event) {
	w.lmu.Lock()
	fns := make([]func(Event), 0, len(w.listeners))
	for _, fn := range w.listeners {
		fns = append(fns, fn)
	}
	w.lmu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}

func (w *worker) snapshotLocked() Event {
	return Event{
		Connector: w.d.Connector,
		Cur:       w.cur,
		Max:       w.max,
		Pct:       pctFromRaw(w.cur, w.max),
		Ready:     w.ready,
		Err:       w.lastErr,
	}
}

// set records the wanted value and returns. The bar renders it right away;
// the bus catches up on its own schedule.
func (w *worker) set(pct int) {
	w.mu.Lock()
	if !w.ready {
		w.mu.Unlock()
		return
	}
	v := rawFromPct(pct, w.max)
	w.pending, w.hasPend = v, true
	w.cur = v
	w.writeGen++
	snap := w.snapshotLocked()
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
	w.emit(snap)
}

// resync forces a re-probe, e.g. after resume from suspend.
func (w *worker) resync() {
	w.mu.Lock()
	w.fails = 0
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// signalStop asks the worker to exit without waiting for it.
//
// The wait is what must not happen on the runtime's goroutine: a wedged
// display can hold a syscall for about a second, and the worker only checks
// quit between bus operations. The fd is released when run() returns either
// way, so nothing is leaked by not watching it happen.
func (w *worker) signalStop() {
	select {
	case <-w.quit:
	default:
		close(w.quit)
	}
}

func (w *worker) stop() {
	w.signalStop()
	<-w.done
}

func (w *worker) run() {
	defer logging.Recover("ddc.worker." + w.d.Connector)
	defer close(w.done)

	t := w.open()
	if t != nil {
		defer t.Close()
	}

	timer := time.NewTimer(w.interval())
	defer timer.Stop()

	for {
		select {
		case <-w.quit:
			return
		case <-w.wake:
			if t == nil {
				continue
			}
			w.flush(t)
		case <-timer.C:
			if t == nil {
				// No transport at all: nothing to retry against.
				return
			}
			w.tick(t)
			timer.Reset(w.interval())
		}
	}
}

// open performs the probe and publishes its outcome.
func (w *worker) open() transport {
	start := time.Now()
	t, err := openTransport(w.d)
	if err != nil {
		w.mu.Lock()
		w.ready, w.lastErr = false, err
		snap := w.snapshotLocked()
		w.mu.Unlock()
		logging.Log.Info().Msgf("ddc: %s: unavailable: %v", w.d.Connector, err)
		w.emit(snap)
		return nil
	}
	if d := time.Since(start); d > probeDeadline {
		logging.Log.Warn().Msgf("ddc: %s: probe took %s", w.d.Connector, d.Round(time.Millisecond))
	}

	cur, max, err := t.Get(VCPLuminance)
	if err != nil {
		t.Close()
		w.mu.Lock()
		w.ready, w.lastErr = false, err
		snap := w.snapshotLocked()
		w.mu.Unlock()
		logging.Log.Info().Msgf("ddc: %s: unusable: %v", w.d.Connector, err)
		w.emit(snap)
		return nil
	}

	w.mu.Lock()
	w.cur, w.max, w.ready, w.lastErr = cur, max, true, nil
	snap := w.snapshotLocked()
	w.mu.Unlock()

	logging.Log.Info().Msgf("ddc: %s: using %s (brightness %d%%, raw %d/%d)",
		w.d.Connector, t.Name(), snap.Pct, cur, max)
	w.emit(snap)
	return t
}

// flush drains the pending slot. Because the slot holds only the newest
// value, a burst of scroll events collapses: the first write goes out at
// once and everything arriving during the mandated post-write delay becomes
// a single further write of the final value.
func (w *worker) flush(t transport) {
	for {
		w.mu.Lock()
		if !w.hasPend || !w.ready {
			w.mu.Unlock()
			return
		}
		v := w.pending
		w.hasPend = false
		w.mu.Unlock()

		if err := t.Set(VCPLuminance, v); err != nil {
			w.fail(err)
			return
		}
		w.ok()
	}
}

// tick decides whether this wake-up should touch the bus.
//
// With polling disabled there is still a slow timer, but only so a display
// that dropped out can be found again — a healthy one is left alone, which
// is what `poll: 0` asks for.
func (w *worker) tick(t transport) {
	w.mu.Lock()
	quiet := w.poll <= 0 && w.ready
	w.mu.Unlock()
	if quiet {
		return
	}
	w.repoll(t)
}

// repoll re-reads the display. It is the only way to notice brightness
// changed with the monitor's own buttons, and the only way a display that
// stopped answering gets to come back.
func (w *worker) repoll(t transport) {
	w.mu.Lock()
	// A pending write is newer than anything a read could tell us.
	if w.hasPend {
		w.mu.Unlock()
		return
	}
	gen := w.writeGen
	w.mu.Unlock()

	cur, max, err := t.Get(VCPLuminance)
	if err != nil {
		w.fail(err)
		return
	}

	w.mu.Lock()
	if w.writeGen != gen {
		// A Set landed while we were reading; the read is stale.
		w.mu.Unlock()
		w.ok()
		return
	}
	changed := w.cur != cur || w.max != max || !w.ready
	w.cur, w.max, w.lastErr, w.ready = cur, max, nil, true
	snap := w.snapshotLocked()
	w.mu.Unlock()

	w.ok()
	if changed {
		w.emit(snap)
	}
}

func (w *worker) ok() {
	w.mu.Lock()
	w.fails = 0
	w.mu.Unlock()
}

func (w *worker) fail(err error) {
	w.mu.Lock()
	w.fails++
	w.lastErr = err
	n := w.fails
	drop := n >= failMax && w.ready
	if drop {
		w.ready = false
	}
	snap := w.snapshotLocked()
	w.mu.Unlock()

	if drop {
		logging.Log.Warn().Msgf("ddc: %s: giving up after %d errors: %v", w.d.Connector, n, err)
		w.emit(snap)
		return
	}
	logging.Log.Debug().Msgf("ddc: %s: %v", w.d.Connector, err)
}

// interval is the poll period, backed off while the display is failing.
func (w *worker) interval() time.Duration {
	w.mu.Lock()
	fails, poll := w.fails, w.poll
	w.mu.Unlock()

	if poll <= 0 {
		// Polling disabled. Still wake occasionally when the display is
		// unhealthy, so it can recover on its own.
		if fails == 0 {
			return backoffMax
		}
	}
	if fails == 0 {
		return poll
	}
	d := backoffMin << min(fails-1, 6)
	return min(d, backoffMax)
}
