// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"
	"sync"
	"time"
)

// Conn is one live connection of a Source. The runtime stops it on module
// teardown and wakes it when the system resumes from suspend.
type Conn interface {
	Stop()
	// Wake is called after resume from suspend so timers realign and
	// pollers resync. Sources with nothing to do may no-op.
	Wake()
}

// Source produces values of T for a module. Modules subscribe in Init via
// On; the runtime opens the source and delivers every emitted value
// serially on the module's goroutine.
type Source[T any] struct {
	open func(emit func(T)) (Conn, error)
}

// NewSource wraps an open function into a Source. open must arrange for
// emit to be called with each new value (typically from its own goroutine;
// emit is safe to call from any goroutine) and return a Conn that stops
// that work. Service packages use this to expose typed event sources.
func NewSource[T any](open func(emit func(T)) (Conn, error)) Source[T] {
	return Source[T]{open: open}
}

// ConnFuncs is a convenience Conn built from plain functions; either may
// be nil.
type ConnFuncs struct {
	StopFn func()
	WakeFn func()
}

func (c ConnFuncs) Stop() {
	if c.StopFn != nil {
		c.StopFn()
	}
}

func (c ConnFuncs) Wake() {
	if c.WakeFn != nil {
		c.WakeFn()
	}
}

// Chan adapts an existing channel into a Source: the escape hatch for
// modules wrapping their own event machinery. Delivery ends when ch is
// closed or the module is stopped.
func Chan[T any](ch <-chan T) Source[T] {
	return NewSource(func(emit func(T)) (Conn, error) {
		done := make(chan struct{})
		var once sync.Once
		go func() {
			for {
				select {
				case v, ok := <-ch:
					if !ok {
						return
					}
					select {
					case <-done:
						return
					default:
					}
					emit(v)
				case <-done:
					return
				}
			}
		}()
		return ConnFuncs{StopFn: func() { once.Do(func() { close(done) }) }}, nil
	})
}

// Tick emits the current time every d. Use a Ticker when the interval must
// change at runtime.
func Tick(d time.Duration) Source[time.Time] { return NewTicker(d).Source() }

// AlignedTick emits the current time every d, aligned to wall-clock
// boundaries (a 1m tick fires at :00 of every minute). It realigns
// automatically after resume from suspend.
func AlignedTick(d time.Duration) Source[time.Time] { return NewAlignedTicker(d).Source() }

// Ticker is a controllable tick source: the interval can be changed while
// running (clock derives it from the active format). A Ticker backs a
// single subscription.
type Ticker struct {
	aligned bool

	mu       sync.Mutex
	interval time.Duration
	nudge    chan struct{}
	inUse    bool
}

// NewTicker returns a steady ticker (first tick one interval from open).
func NewTicker(d time.Duration) *Ticker {
	return &Ticker{interval: d, nudge: make(chan struct{}, 1)}
}

// NewAlignedTicker returns a boundary-aligned ticker.
func NewAlignedTicker(d time.Duration) *Ticker {
	return &Ticker{aligned: true, interval: d, nudge: make(chan struct{}, 1)}
}

// Interval returns the current tick interval.
func (t *Ticker) Interval() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.interval
}

// Set changes the tick interval; the next tick is rescheduled immediately.
// Setting the current interval is a no-op.
func (t *Ticker) Set(d time.Duration) {
	t.mu.Lock()
	changed := t.interval != d
	t.interval = d
	t.mu.Unlock()
	if changed {
		t.poke()
	}
}

func (t *Ticker) poke() {
	select {
	case t.nudge <- struct{}{}:
	default:
	}
}

// Source returns the tick source. Each Ticker supports one subscription.
func (t *Ticker) Source() Source[time.Time] {
	return NewSource(func(emit func(time.Time)) (Conn, error) {
		t.mu.Lock()
		if t.inUse {
			t.mu.Unlock()
			return nil, fmt.Errorf("ticker already subscribed")
		}
		t.inUse = true
		t.mu.Unlock()

		done := make(chan struct{})
		var once sync.Once
		go func() {
			for {
				t.mu.Lock()
				d := t.interval
				t.mu.Unlock()
				if d <= 0 {
					d = time.Second
				}
				var next time.Time
				now := time.Now()
				if t.aligned {
					next = now.Truncate(d).Add(d)
				} else {
					next = now.Add(d)
				}
				timer := time.NewTimer(time.Until(next))
				select {
				case now := <-timer.C:
					emit(now)
				case <-t.nudge:
					timer.Stop()
				case <-done:
					timer.Stop()
					return
				}
			}
		}()
		return ConnFuncs{
			StopFn: func() { once.Do(func() { close(done) }) },
			WakeFn: t.poke, // recompute next boundary after suspend
		}, nil
	})
}

// Subscription is the type-erased pairing of an open source and its
// handler, managed by the runtime. Module authors never construct one
// directly; On does.
type Subscription struct {
	open    func(emit func(any)) (Conn, error)
	deliver func(any)

	mu   sync.Mutex
	conn Conn
}

// On subscribes the module to src: every value src emits is delivered
// serially on the module goroutine via fn, and a redraw follows
// automatically. Call it from Init (or later hooks; new subscriptions are
// opened immediately). The returned Subscription can be Closed early;
// otherwise the runtime closes it at module teardown.
func On[T any](ctx *Ctx, src Source[T], fn func(T)) *Subscription {
	s := &Subscription{
		open: func(emit func(any)) (Conn, error) {
			return src.open(func(v T) { emit(v) })
		},
		deliver: func(v any) { fn(v.(T)) },
	}
	ctx.addSubscription(s)
	return s
}

// Open starts the subscription's source. Runtime use.
func (s *Subscription) Open(emit func(any)) error {
	conn, err := s.open(emit)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	return nil
}

// Deliver invokes the handler with an emitted value. Runtime use: must be
// called on the module goroutine.
func (s *Subscription) Deliver(v any) { s.deliver(v) }

// Wake forwards a system resume to the source. Runtime use.
func (s *Subscription) Wake() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		conn.Wake()
	}
}

// Close stops the subscription's source; further values are not delivered.
func (s *Subscription) Close() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		conn.Stop()
	}
}
