// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"github.com/nekorg/katnip"
)

// kittyOverrides is the one copy of the kitty tuning every menu panel
// (and historically each menu package) needs: pinned font size so cell
// metrics match the bar, no zoom/cursor noise, no dangerous paste.
var kittyOverrides = []string{
	"font_size=12",
	"cursor_trail=0",
	"cursor_shape=beam",
	"cursor=#000000",
	"paste_actions=replace-dangerous-control-codes",
	"map kitty_mod+equal       no_op",
	"map kitty_mod+plus        no_op",
	"map kitty_mod+kp_add      no_op",
	"map cmd+plus              no_op",
	"map cmd+equal             no_op",
	"map shift+cmd+equal       no_op",
	"map kitty_mod+minus       no_op",
	"map kitty_mod+kp_subtract no_op",
	"map cmd+minus             no_op",
	"map shift+cmd+minus       no_op",
	"map kitty_mod+backspace   no_op",
	"map cmd+0                 no_op",
	"draw_minimal_borders=yes",
	// Pin padding to zero so a panel's on-screen footprint is exactly
	// its cells; clamping then only has to account for panelPad, not
	// unknown chrome.
	"window_padding_width=0",
}

const (
	// warmCols/warmRows are the spare's placeholder size. The host waits
	// for the panel to settle at exactly this size before announcing
	// readiness: a freshly mapped panel first reports a wrong-scale
	// transient, then settles to its configured size at the output's real
	// scale — rendering before that would lay out at the wrong metrics.
	warmCols = 20
	warmRows = 5
	// offScreenMargin parks a warm spare this many logical units past the
	// output's right edge, where the compositor maps it invisibly. The menu
	// is brought on-screen by a later Move (the reveal).
	offScreenMargin = 2000
)

// spawnHostPanel starts a warm menu-host panel: hidden, pinned to the
// primary output (so its cell scale matches the eventual on-screen scale
// even while parked off it), parked past that output's right edge, with a
// non-grabbing focus policy so an idle spare never steals the keyboard.
// The host maps itself off-screen (paying the map round-trip while idle)
// and switches to exclusive focus only when a menu reveals it. Size is a
// placeholder — the host resizes to the real menu on open.
func spawnHostPanel() (*katnip.Panel, error) {
	cfg := katnip.Config{
		Position:       katnip.Vector{X: 100000, Y: 0}, // fallback park when the output is unknown
		Size:           katnip.Vector{X: warmCols, Y: warmRows},
		Edge:           katnip.EdgeNone,
		Layer:          katnip.LayerTop,
		FocusPolicy:    katnip.FocusNotAllowed,
		ConfigFile:     "NONE",
		StartAsHidden:  true,
		KittyOverrides: kittyOverrides,
	}
	if mon, ok := output(); ok {
		cfg.OutputName = mon.Name
		if mon.ScaledWidth > 0 {
			cfg.Position.X = mon.ScaledWidth + offScreenMargin
		}
	}
	kn := katnip.NewPanel(hostInstance, cfg)
	if err := kn.Start(); err != nil {
		return nil, err
	}
	return kn, nil
}
