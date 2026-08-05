// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ddc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTransport stands in for a display, with a settable per-operation cost
// so the tests can model a bus that is slow relative to the event rate.
type fakeTransport struct {
	mu       sync.Mutex
	cur, max uint16
	sets     []uint16
	gets     int

	setDelay time.Duration
	getDelay time.Duration
	setErr   error

	// onGet runs inside Get, before it returns, to stage a race.
	onGet func()
}

func (f *fakeTransport) Name() string { return "fake" }

func (f *fakeTransport) Get(vcp byte) (uint16, uint16, error) {
	f.mu.Lock()
	f.gets++
	cur, max, delay, hook := f.cur, f.max, f.getDelay, f.onGet
	f.mu.Unlock()

	if hook != nil {
		hook()
	}
	time.Sleep(delay)
	return cur, max, nil
}

func (f *fakeTransport) Set(vcp byte, v uint16) error {
	f.mu.Lock()
	delay, err := f.setDelay, f.setErr
	f.mu.Unlock()

	time.Sleep(delay)

	f.mu.Lock()
	if err == nil {
		f.sets = append(f.sets, v)
		f.cur = v
	}
	f.mu.Unlock()
	return err
}

func (f *fakeTransport) Close() error { return nil }

func (f *fakeTransport) writes() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.sets...)
}

// stubTransport makes openTransport hand out t.
func stubTransport(t *testing.T, ft transport, err error) {
	t.Helper()
	old := openTransport
	openTransport = func(Display) (transport, error) { return ft, err }
	t.Cleanup(func() { openTransport = old })
}

func startWorker(t *testing.T, ft transport, poll time.Duration) *worker {
	t.Helper()
	stubTransport(t, ft, nil)

	w := newWorker(Display{Connector: "DP-1"}, poll)
	go w.run()
	t.Cleanup(w.stop)

	deadline := time.Now().Add(2 * time.Second)
	for {
		w.mu.Lock()
		ready := w.ready
		w.mu.Unlock()
		if ready {
			return w
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never became ready")
		}
		time.Sleep(time.Millisecond)
	}
}

// The headline property: a scroll burst must not queue up one bus write per
// event. Only the newest value matters, so the intermediate ones are dropped.
func TestWorkerCoalescesWrites(t *testing.T) {
	ft := &fakeTransport{cur: 50, max: 100, setDelay: 20 * time.Millisecond}
	w := startWorker(t, ft, time.Hour) // no polling in the way

	const last = 98
	for pct := 0; pct <= last; pct += 2 {
		w.set(pct)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		ws := ft.writes()
		if len(ws) > 0 && ws[len(ws)-1] == last {
			// A 50-event burst against a 20ms bus should settle in a
			// couple of writes; anything near 50 means no coalescing.
			if len(ws) > 6 {
				t.Errorf("burst of 50 events produced %d bus writes (%v), want a handful", len(ws), ws)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("final value never reached the bus: writes = %v", ft.writes())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// set() runs on the module goroutine, so it must never wait on the bus.
func TestWorkerSetDoesNotBlock(t *testing.T) {
	ft := &fakeTransport{cur: 50, max: 100, setDelay: 200 * time.Millisecond}
	w := startWorker(t, ft, time.Hour)

	start := time.Now()
	for pct := range 50 {
		w.set(pct)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("50 set() calls took %s against a 200ms bus; they must not wait on it", d)
	}
}

// The rendered value updates from the Set itself, not from a later read.
func TestWorkerSetUpdatesStateImmediately(t *testing.T) {
	ft := &fakeTransport{cur: 50, max: 100, setDelay: time.Second}
	w := startWorker(t, ft, time.Hour)

	var got Event
	var mu sync.Mutex
	w.listen(func(ev Event) {
		mu.Lock()
		got = ev
		mu.Unlock()
	})

	w.set(77)

	mu.Lock()
	defer mu.Unlock()
	if got.Pct != 77 {
		t.Errorf("emitted Pct = %d, want 77 without waiting for the 1s write", got.Pct)
	}
}

// A read that started before a Set is stale by the time it lands. Applying
// it anyway would snap the bar back to the pre-scroll brightness.
func TestWorkerDiscardsReadRacedByWrite(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, time.Hour)
	w.cur, w.max, w.ready = 50, 100, true

	ft := &fakeTransport{cur: 10, max: 100, getDelay: 20 * time.Millisecond}
	// A scroll lands while the poll read is in flight.
	ft.onGet = func() { w.set(80) }

	w.repoll(ft)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur != 80 {
		t.Errorf("cur = %d, want 80: the in-flight read (10) must lose to the newer write", w.cur)
	}
}

// A poll with no write racing it must apply, or OSD button changes never show.
func TestWorkerRepollAppliesExternalChange(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, time.Hour)
	w.cur, w.max, w.ready = 50, 100, true

	var got Event
	var mu sync.Mutex
	w.listen(func(ev Event) {
		mu.Lock()
		got = ev
		mu.Unlock()
	})

	w.repoll(&fakeTransport{cur: 33, max: 100})

	mu.Lock()
	defer mu.Unlock()
	if got.Pct != 33 {
		t.Errorf("emitted Pct = %d, want 33", got.Pct)
	}
}

// A pending write is newer than anything a read could report, so polling
// must not clobber it.
func TestWorkerRepollSkipsWhenWritePending(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, time.Hour)
	w.cur, w.max, w.ready = 50, 100, true
	w.pending, w.hasPend = 80, true

	ft := &fakeTransport{cur: 10, max: 100}
	w.repoll(ft)

	if ft.gets != 0 {
		t.Errorf("read the bus %d times with a write pending, want 0", ft.gets)
	}
}

// Repeated failures must stop the display without killing the worker: a
// monitor that is merely asleep has to be able to come back.
func TestWorkerBacksOffOnFailure(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, 10*time.Millisecond)
	w.cur, w.max, w.ready = 50, 100, true

	boom := errors.New("bus error")
	for range failMax {
		w.fail(boom)
	}

	w.mu.Lock()
	ready := w.ready
	w.mu.Unlock()
	if ready {
		t.Errorf("still ready after %d consecutive failures", failMax)
	}
	if got := w.interval(); got <= 10*time.Millisecond {
		t.Errorf("interval = %s, want a backed-off value greater than the poll period", got)
	}

	w.ok()
	if got := w.interval(); got != 10*time.Millisecond {
		t.Errorf("interval after recovery = %s, want the poll period back", got)
	}
}

// A display marked dead has to be able to come back — a monitor that was
// merely switched off or asleep is the common case.
func TestWorkerRecoversAfterFailure(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, 10*time.Millisecond)
	w.cur, w.max, w.ready = 50, 100, true

	for range failMax {
		w.fail(errors.New("bus error"))
	}
	w.mu.Lock()
	dead := !w.ready
	w.mu.Unlock()
	if !dead {
		t.Fatal("precondition: display should have been marked unusable")
	}

	w.repoll(&fakeTransport{cur: 60, max: 100})

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ready {
		t.Error("a successful read did not bring the display back")
	}
	if w.cur != 60 {
		t.Errorf("cur = %d, want 60", w.cur)
	}
}

// poll: 0 means "do not go near the bus on a timer".
func TestWorkerTickQuietWhenPollDisabled(t *testing.T) {
	w := newWorker(Display{Connector: "DP-1"}, 0)
	w.cur, w.max, w.ready = 50, 100, true

	ft := &fakeTransport{cur: 10, max: 100}
	w.tick(ft)

	if ft.gets != 0 {
		t.Errorf("polled the bus %d times with poll disabled, want 0", ft.gets)
	}

	// ...but a display that dropped out still gets looked for.
	w.mu.Lock()
	w.ready = false
	w.mu.Unlock()
	w.tick(ft)

	if ft.gets == 0 {
		t.Error("never retried an unusable display, so it can never recover")
	}
}

// A display that never answered must report the failure rather than sit
// silently, and must not accept writes.
func TestWorkerUnavailableDisplay(t *testing.T) {
	stubTransport(t, nil, errors.New("no DDC/CI display"))

	w := newWorker(Display{Connector: "DP-1"}, time.Hour)
	var got Event
	var mu sync.Mutex
	done := make(chan struct{})
	var once sync.Once
	w.listen(func(ev Event) {
		mu.Lock()
		got = ev
		mu.Unlock()
		once.Do(func() { close(done) })
	})

	go w.run()
	t.Cleanup(w.stop)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no event for an unavailable display")
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Ready || got.Err == nil {
		t.Errorf("got %+v, want Ready=false with an error", got)
	}
}
