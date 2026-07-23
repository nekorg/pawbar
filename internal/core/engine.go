// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package core

import (
	"sync"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"github.com/rs/zerolog"
	"go.rockorager.dev/vaxis"
)

// Side indexes the three bar anchors.
type Side int

const (
	Left Side = iota
	Middle
	Right
)

// Update is a module's fresh render snapshot, addressed by slot.
type Update struct {
	Side  Side
	Index int
	Segs  []module.Segment
}

// Engine owns all runners and routes input to them. Its exported methods
// are meant for the single main loop goroutine.
type Engine struct {
	log     zerolog.Logger
	updates chan Update

	// mu guards sides for the one cross-goroutine reader (push, from
	// runner goroutines); all mutation happens on the main goroutine.
	mu    sync.RWMutex
	sides [3][]*runner

	restarts chan SlotRef

	hover       *runner
	hoverRegion string
	pressed     *runner
}

// New builds an engine for a compiled bar. Call Start to launch modules.
func New(bar *config.Bar, log zerolog.Logger) *Engine {
	e := &Engine{
		log:      log,
		updates:  make(chan Update, 64),
		restarts: make(chan SlotRef, 16),
	}
	build := func(side Side, insts []*config.Instance) []*runner {
		rs := make([]*runner, len(insts))
		for i, inst := range insts {
			rs[i] = newRunner(e, side, i, inst)
		}
		return rs
	}
	e.sides[Left] = build(Left, bar.Left)
	e.sides[Middle] = build(Middle, bar.Middle)
	e.sides[Right] = build(Right, bar.Right)
	return e
}

// SlotCounts returns the number of slots per side, for layout setup.
func (e *Engine) SlotCounts() (l, m, r int) {
	return len(e.sides[Left]), len(e.sides[Middle]), len(e.sides[Right])
}

// Updates delivers render snapshots; the main loop drains it.
func (e *Engine) Updates() <-chan Update { return e.updates }

// Snapshots invokes fn with every slot's last published segments. The
// main loop uses it to reseed the layout after a reload rebuilt the slot
// tables: kept runners only push updates when their output changes, so
// without reseeding they would stay blank until their next event.
func (e *Engine) Snapshots(fn func(side Side, idx int, segs []module.Segment)) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for s, side := range e.sides {
		for i, r := range side {
			r.segsMu.Lock()
			segs := r.lastSegs
			r.segsMu.Unlock()
			if segs != nil {
				fn(Side(s), i, segs)
			}
		}
	}
}

// Start launches every runner; initial snapshots arrive on Updates.
func (e *Engine) Start() {
	for _, side := range e.sides {
		for _, r := range side {
			r.start()
		}
	}
}

// Stop tears all runners down (reverse order, so rightmost disappears
// first; cosmetic, but deterministic).
func (e *Engine) Stop() {
	for _, side := range e.sides {
		for i := len(side) - 1; i >= 0; i-- {
			side[i].stop()
		}
	}
}

// Wake broadcasts resume-from-suspend to every runner's sources.
func (e *Engine) Wake() {
	for _, side := range e.sides {
		for _, r := range side {
			r.wake()
		}
	}
}

// push delivers a runner's snapshot, dropping it when the runner is no
// longer the live occupant of its slot (stale post-reload publishes).
func (e *Engine) push(r *runner, u Update) {
	e.mu.RLock()
	s := e.sides[u.Side]
	live := u.Index < len(s) && s[u.Index] == r
	e.mu.RUnlock()
	if !live {
		return
	}
	select {
	case e.updates <- u:
	case <-r.done:
	}
}

func (e *Engine) runnerAt(side Side, idx int) *runner {
	s := e.sides[side]
	if idx < 0 || idx >= len(s) {
		return nil
	}
	return s[idx]
}

// PointerLeft handles the pointer leaving the bar entirely.
func (e *Engine) PointerLeft() {
	e.setHover(nil, "")
}

// clearPointer forgets hover/pressed references to a runner about to be
// stopped or replaced.
func (e *Engine) clearPointer(r *runner) {
	if e.hover == r {
		e.hover, e.hoverRegion = nil, ""
	}
	if e.pressed == r {
		e.pressed = nil
	}
}

// Mouse routes a vaxis mouse event to the slot under the pointer.
// hitOK=false means the pointer is over empty bar space.
func (e *Engine) Mouse(side Side, idx int, region string, ev vaxis.Mouse, hitOK bool) {
	var cur *runner
	if hitOK {
		cur = e.runnerAt(side, idx)
	}
	e.setHover(cur, region)
	if cur == nil || cur.broken.Load() {
		return
	}

	switch ev.EventType {
	case vaxis.EventPress:
		btn := buttonName(ev)
		if btn == "" {
			return
		}
		if !isScroll(btn) {
			e.setPressed(cur, true)
		}
		e.runActions(cur, btn, region, ev.XPixel, ev.YPixel)
		e.postMouse(cur, ev, btn, "press", region)
	case vaxis.EventRelease:
		e.setPressed(cur, false)
		if btn := buttonName(ev); btn != "" && !isScroll(btn) {
			e.postMouse(cur, ev, btn, "release", region)
		}
	}
}

func (e *Engine) setHover(cur *runner, region string) {
	prev, prevRegion := e.hover, e.hoverRegion
	if prev == cur && prevRegion == region {
		return
	}
	e.hover, e.hoverRegion = cur, region

	if prev != nil && prev != cur {
		if e.pressed == prev {
			e.setPressed(prev, false)
		}
		prev.post(func() {
			prev.setState(config.HoverState, false)
			if ho, ok := prev.mod.(module.HoverObserver); ok {
				ho.OnHover(prev.ctx, false, prevRegion)
			}
		})
		e.runActionsOff(prev)
	}
	if cur != nil && (cur != prev || region != prevRegion) && !cur.broken.Load() {
		entered := cur != prev
		cur.post(func() {
			cur.setState(config.HoverState, true)
			if ho, ok := cur.mod.(module.HoverObserver); ok {
				ho.OnHover(cur.ctx, true, region)
			}
		})
		if entered {
			e.runActions(cur, "hover", region, 0, 0)
		}
	}
}

func (e *Engine) setPressed(r *runner, on bool) {
	if on {
		e.pressed = r
	} else if e.pressed == r {
		e.pressed = nil
	} else if !on {
		return
	}
	r.post(func() { r.setState(config.PressedState, on) })
}

func (e *Engine) postMouse(r *runner, ev vaxis.Mouse, btn, kind, region string) {
	if _, ok := r.mod.(module.MouseHandler); !ok {
		return
	}
	m := module.Mouse{
		Button: btn,
		Kind:   kind,
		Region: region,
		Col:    ev.Col,
		XPixel: ev.XPixel,
		YPixel: ev.YPixel,
	}
	r.post(func() {
		if mh, ok := r.mod.(module.MouseHandler); ok {
			mh.OnMouse(r.ctx, m)
		}
	})
}

func buttonName(ev vaxis.Mouse) string {
	switch ev.Button {
	case vaxis.MouseLeftButton:
		return "left"
	case vaxis.MouseRightButton:
		return "right"
	case vaxis.MouseMiddleButton:
		return "middle"
	case vaxis.MouseWheelUp:
		return "scroll-up"
	case vaxis.MouseWheelDown:
		return "scroll-down"
	case 66:
		return "scroll-left"
	case 67:
		return "scroll-right"
	default:
		return ""
	}
}

func isScroll(btn string) bool {
	return len(btn) > 7 && btn[:7] == "scroll-"
}
