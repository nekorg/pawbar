// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package menus is pawbar's menu framework. It owns everything a menu
// needs besides its content: the kitty panel boilerplate, screen-edge
// clamping, one-menu-at-a-time and toggle-on-reclick semantics,
// close-on-focus-loss, and Esc handling.
//
// Two layers:
//
//   - Open runs a custom TUI (an AppFunc registered with Register) for
//     menus that draw themselves, like the calendar.
//   - OpenList (list.go) renders a declarative item list with actions,
//     toggles and submenus; most menus want this one.
package menus

import (
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/module"
)

// FromVerb anchors a menu at the click that invoked a verb.
func FromVerb(a module.VerbArgs) Anchor { return Anchor{a.XPixel, a.YPixel} }

// FromMouse anchors a menu at a raw mouse event.
func FromMouse(ev module.Mouse) Anchor { return Anchor{ev.XPixel, ev.YPixel} }

// Spec describes a custom (self-drawing) menu.
type Spec struct {
	// Name of the AppFunc registered with Register.
	Name string
	// Size in cells.
	Width, Height int
	// Key subdivides the opening module for toggle purposes; leave
	// empty when the module has a single menu.
	Key string
	// NoAutoClose keeps the menu open when it loses focus.
	NoAutoClose bool
	// OnOpen, if set, runs on its own goroutine with the live handle,
	// for menus that talk a custom wire protocol.
	OnOpen func(h *Handle)
}

// Open opens (or toggles) spec's menu near the anchor, clamped to the
// monitor. It returns immediately; spawning and lifetime run on a
// runtime-tracked goroutine.
func Open(ctx *module.Ctx, at Anchor, spec Spec) error {
	ctx.Go(func() {
		openAndWait(owner{id: ctx, key: spec.Key}, at, spec)
	})
	return nil
}

// openAndWait drives one menu lifetime; it blocks until the menu is
// gone, so callers run it off the module goroutine.
func openAndWait(o owner, at Anchor, spec Spec) {
	h, err := openRoot(o, spec.Name, at, spec.Width, spec.Height, !spec.NoAutoClose)
	if err != nil {
		logging.Log.Error().Msgf("menus: opening %q: %v", spec.Name, err)
		return
	}
	if h == nil {
		return // toggled closed
	}
	if spec.OnOpen != nil {
		go spec.OnOpen(h)
	}
	<-h.Done()
}
