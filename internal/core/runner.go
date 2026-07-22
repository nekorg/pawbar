// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package core is pawbar's module runtime: it owns every module's
// goroutine, event delivery, state resolution, redraw pipeline and
// lifecycle. Module authors never see any of this; they write against
// pkg/module.
package core

import (
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
)

const (
	mailboxCap    = 16
	stopTimeout   = 2 * time.Second
	panicParkTime = 10 * time.Second
	settleLimit   = 8
)

// runner drives one module instance. Every hook, source delivery, verb and
// state change executes on its single goroutine via the mailbox.
type runner struct {
	eng  *Engine
	side Side
	idx  int
	// inst is swapped on hot reload (runner goroutine) and read from both
	// the runner and the main goroutine; access it through in().
	inst atomic.Pointer[config.Instance]

	mod module.Module
	ctx *module.Ctx

	mailbox  chan func()
	done     chan struct{}
	doneOnce sync.Once
	loopWG   sync.WaitGroup

	subsMu sync.Mutex
	subs   []*module.Subscription
	open   bool // subscriptions may be opened (loop is running)

	// broken is set by the runner goroutine (or start) and read by the
	// engine to gate input routing.
	broken atomic.Bool

	// pending carries a hot-reload config swap to the runner goroutine
	// without a blocking post (the mailbox may be full mid-reload).
	pending atomic.Pointer[pendingConfig]

	// Everything below is touched only on the runner goroutine.
	active      []string
	statesDirty bool
	brokenMsg   string
	lastPanic   time.Time

	segsMu   sync.Mutex
	lastSegs []module.Segment
}

func newRunner(eng *Engine, side Side, idx int, inst *config.Instance) *runner {
	r := &runner{
		eng:     eng,
		side:    side,
		idx:     idx,
		mailbox: make(chan func(), mailboxCap),
		done:    make(chan struct{}),
	}
	r.inst.Store(inst)
	return r
}

// in returns the runner's current instance configuration.
func (r *runner) in() *config.Instance { return r.inst.Load() }

type pendingConfig struct {
	inst     *config.Instance
	onConfig bool
}

// applyConfig schedules a config swap onto the runner goroutine. Never
// blocks: reload must not deadlock against a full mailbox.
func (r *runner) applyConfig(inst *config.Instance, onConfig bool) {
	r.pending.Store(&pendingConfig{inst: inst, onConfig: onConfig})
	r.postNudge()
}

// applyPending performs a scheduled config swap. Runner goroutine only.
func (r *runner) applyPending() {
	p := r.pending.Swap(nil)
	if p == nil {
		return
	}
	r.inst.Store(p.inst)
	if r.broken.Load() {
		return
	}
	opts, err := p.inst.Table.ResolveOptions(r.active)
	if err != nil {
		r.fail(err.Error())
		return
	}
	r.ctx.SetOptions(opts)
	if !p.onConfig {
		return
	}
	if rc, ok := r.mod.(module.Reconfigurer); ok {
		if err := rc.OnConfig(r.ctx); err != nil {
			r.eng.logf("%s: OnConfig: %v; restarting", p.inst.Name, err)
			r.eng.requestRestart(r)
		}
	}
}

func (r *runner) start() {
	if r.in().Err != nil {
		r.park(r.in().Err.Error())
		return
	}

	r.mod = r.in().Def.New()
	opts, err := r.in().Table.ResolveOptions(nil)
	if err != nil {
		r.park(err.Error())
		return
	}
	r.ctx = module.NewCtx(hostFor(r), opts)

	r.subsMu.Lock()
	r.open = true
	r.subsMu.Unlock()

	r.loopWG.Add(1)
	go r.loop()

	r.post(func() {
		if err := r.mod.Init(r.ctx); err != nil {
			r.fail(fmt.Sprintf("init: %v", err))
		}
	})
}

func (r *runner) loop() {
	defer r.loopWG.Done()
	for {
		select {
		case <-r.done:
			return
		default:
		}
		select {
		case <-r.done:
			return
		case fn := <-r.mailbox:
			r.exec(r.applyPending)
			r.exec(fn)
			r.settle()
			r.render()
		}
	}
}

// post enqueues work for the runner goroutine. It drops the item when the
// runner is stopping.
func (r *runner) post(fn func()) {
	select {
	case r.mailbox <- fn:
	case <-r.done:
	}
}

// postNudge schedules a render without blocking; used from the runner's
// own goroutine (Refresh) where a blocking post could deadlock.
func (r *runner) postNudge() {
	select {
	case r.mailbox <- func() {}:
	default:
	}
}

func (r *runner) exec(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			r.eng.logf("%s: panic: %v\n%s", r.in().Name, p, debug.Stack())
			now := time.Now()
			if !r.lastPanic.IsZero() && now.Sub(r.lastPanic) < panicParkTime {
				r.fail(fmt.Sprintf("repeated panic: %v", p))
			}
			r.lastPanic = now
		}
	}()
	fn()
}

// park puts a never-started slot straight into chip mode: the loop never
// runs, done is closed so posts fall through instead of filling the
// mailbox.
func (r *runner) park(msg string) {
	r.broken.Store(true)
	r.brokenMsg = msg
	// Publish before closing done: push gives up on stopped runners.
	r.publish(r.chipSegments())
	r.closeDone()
}

// fail parks the module and shows its error chip. Runner goroutine only.
func (r *runner) fail(msg string) {
	r.broken.Store(true)
	r.brokenMsg = msg
	r.eng.logf("%s: %s", r.in().Name, msg)
}

// settle applies pending state changes: re-resolve options, notify the
// module, repeat if the module flipped more states in response.
func (r *runner) settle() {
	for range settleLimit {
		if !r.statesDirty || r.broken.Load() {
			return
		}
		r.statesDirty = false
		opts, err := r.in().Table.ResolveOptions(r.active)
		if err != nil {
			r.fail(err.Error())
			return
		}
		r.ctx.SetOptions(opts)
		if so, ok := r.mod.(module.StateObserver); ok {
			so.OnState(r.ctx)
		}
	}
	r.eng.logf("%s: state churn: OnState kept flipping states, giving up", r.in().Name)
}

func (r *runner) render() {
	if r.broken.Load() {
		r.publish(r.chipSegments())
		return
	}
	w := module.NewWriter(func(extra []string) module.Resolved {
		states := r.active
		if len(extra) > 0 {
			states = append(slices.Clone(r.active), extra...)
		}
		b := r.in().Table.ResolveBlock(states)
		return module.Resolved{Style: b.Style(), Shape: b.Shape(), Formatter: b.Formatter()}
	})
	r.mod.Render(w)
	if err := w.Err(); err != nil {
		r.eng.logf("%s: render: %v", r.in().Name, err)
	}
	r.publish(w.Segments())
}

func (r *runner) publish(segs []module.Segment) {
	r.segsMu.Lock()
	same := slices.Equal(r.lastSegs, segs)
	if !same {
		r.lastSegs = segs
	}
	r.segsMu.Unlock()
	if !same {
		r.eng.push(r, Update{Side: r.side, Index: r.idx, Segs: segs})
	}
}

func (r *runner) chipSegments() []module.Segment {
	return []module.Segment{{
		Text:   "⚠" + r.in().Name,
		Style:  vaxis.Style{Foreground: vaxis.IndexColor(9)},
		Region: "error",
		Shape:  vaxis.MouseShapeHelp,
	}}
}

// setState flips a state; runner goroutine only.
func (r *runner) setState(name string, on bool) {
	if r.in().Table == nil || !r.in().Table.Known(name) {
		r.eng.logf("%s: unknown state %q", r.in().Name, name)
		return
	}
	i := slices.Index(r.active, name)
	if on == (i >= 0) {
		return
	}
	if on {
		r.active = append(r.active, name)
	} else {
		r.active = slices.Delete(r.active, i, i+1)
	}
	r.statesDirty = true
}

// stop tears the module down: sources first, then the Stop hook.
func (r *runner) stop() {
	r.subsMu.Lock()
	r.open = false
	subs := slices.Clone(r.subs)
	r.subs = nil
	r.subsMu.Unlock()

	r.closeDone()
	for _, s := range subs {
		s.Close()
	}

	if r.mod == nil {
		return
	}

	loopDone := make(chan struct{})
	go func() { r.loopWG.Wait(); close(loopDone) }()
	select {
	case <-loopDone:
	case <-time.After(stopTimeout):
		r.eng.logf("%s: stop: goroutine did not settle in %v", r.in().Name, stopTimeout)
		return
	}

	if st, ok := r.mod.(module.Stopper); ok {
		fin := make(chan struct{})
		go func() {
			defer close(fin)
			defer func() {
				if p := recover(); p != nil {
					r.eng.logf("%s: Stop panic: %v", r.in().Name, p)
				}
			}()
			st.Stop(r.ctx)
		}()
		select {
		case <-fin:
		case <-time.After(stopTimeout):
			r.eng.logf("%s: Stop hook timed out", r.in().Name)
		}
	}
}

func (r *runner) closeDone() {
	r.doneOnce.Do(func() { close(r.done) })
}

// wake forwards resume-from-suspend to every subscription and schedules a
// re-render.
func (r *runner) wake() {
	r.subsMu.Lock()
	subs := slices.Clone(r.subs)
	r.subsMu.Unlock()
	for _, s := range subs {
		s.Wake()
	}
	r.post(func() {})
}

// host adapts runner to module.Host. Methods are only legal on the runner
// goroutine (they are called from module hooks, which the runner runs).
type host struct{ r *runner }

func hostFor(r *runner) module.Host { return host{r} }

func (h host) Name() string { return h.r.in().Name }

func (h host) Logf(format string, args ...any) {
	h.r.eng.logf(h.r.in().Name+": "+format, args...)
}

func (h host) SetState(name string, on bool) { h.r.setState(name, on) }

func (h host) States() []string { return slices.Clone(h.r.active) }

func (h host) Block() module.Block { return h.r.in().Table.ResolveBlock(h.r.active) }

func (h host) Refresh() { h.r.postNudge() }

func (h host) Go(fn func()) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				h.r.eng.logf("%s: goroutine panic: %v", h.r.in().Name, p)
			}
		}()
		fn()
	}()
}

func (h host) SubscriptionAdded(s *module.Subscription) {
	r := h.r
	r.subsMu.Lock()
	if !r.open {
		r.subsMu.Unlock()
		return
	}
	r.subs = append(r.subs, s)
	r.subsMu.Unlock()

	err := s.Open(func(v any) {
		r.post(func() { s.Deliver(v) })
	})
	if err != nil {
		r.eng.logf("%s: source: %v", r.in().Name, err)
	}
}
