// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package core

import (
	"os/exec"
	"slices"

	"github.com/nekorg/pawbar/pkg/module"
)

// runActions executes the config `on:` bindings for a button (or "hover")
// on the target runner. x/y are the pointer position in pixels, zero for
// non-pointer invocations.
func (e *Engine) runActions(r *runner, button, region string, x, y int) {
	acts := r.in().On[button]
	if len(acts) == 0 {
		return
	}
	r.post(func() {
		for _, a := range acts {
			e.runAction(r, a, region, x, y)
		}
	})
}

// runActionsOff runs the implicit inverse of hover `set:` bindings when
// the pointer leaves: a hover-set state is a while-hovered state.
func (e *Engine) runActionsOff(r *runner) {
	acts := r.in().On["hover"]
	if len(acts) == 0 {
		return
	}
	r.post(func() {
		for _, a := range acts {
			if a.Set != "" {
				r.setState(a.Set, false)
			}
		}
	})
}

// runAction executes one action on the runner goroutine.
func (e *Engine) runAction(r *runner, a module.Action, region string, x, y int) {
	switch {
	case a.Verb != "":
		fn, ok := r.ctx.Verb(a.Verb)
		if !ok {
			e.log.Warn().Str("module", r.in().Name).Msgf("verb %q declared but not bound in Init", a.Verb)
			return
		}
		if err := fn(module.VerbArgs{Args: a.Args, Region: region, XPixel: x, YPixel: y}); err != nil {
			e.log.Error().Str("module", r.in().Name).Msgf("%s: %v", a.Verb, err)
		}

	case len(a.Run) > 0:
		argv := a.Run
		r.ctx.Go(func() {
			if err := exec.Command(argv[0], argv[1:]...).Start(); err != nil {
				e.log.Error().Str("module", r.in().Name).Msgf("run %v: %v", argv, err)
			}
		})

	case a.Notify != "":
		msg := a.Notify
		r.ctx.Go(func() {
			_ = exec.Command("notify-send", msg).Start()
		})

	case a.Set != "":
		on := !slices.Contains(r.active, a.Set)
		r.setState(a.Set, on)

	case len(a.Cycle) > 0:
		e.cycleStates(r, a.Cycle)
	}
}

// cycleStates advances through a state list: none active -> first,
// active[i] -> i+1, last -> all off.
func (e *Engine) cycleStates(r *runner, cycle []string) {
	cur := -1
	for i, s := range cycle {
		if slices.Contains(r.active, s) {
			cur = i
			break
		}
	}
	if cur >= 0 {
		r.setState(cycle[cur], false)
	}
	if next := cur + 1; next < len(cycle) {
		r.setState(cycle[next], true)
	}
}
