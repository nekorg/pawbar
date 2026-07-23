// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
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
}

// spawnPanel creates and starts a menu panel at a logical position with a
// size in cells. x/y must already be clamped.
func spawnPanel(name string, x, y, wCells, hCells int) (*katnip.Panel, error) {
	kn := katnip.NewPanel(name, katnip.Config{
		Position:       katnip.Vector{X: x, Y: y},
		Size:           katnip.Vector{X: wCells, Y: hCells},
		Edge:           katnip.EdgeNone,
		Layer:          katnip.LayerTop,
		FocusPolicy:    katnip.FocusExclusive,
		ConfigFile:     "NONE",
		KittyOverrides: kittyOverrides,
	})
	logging.Log.Debug().Msgf("menus: spawning %q at (%d,%d) %dx%d cells", name, x, y, wCells, hCells)
	if err := kn.Start(); err != nil {
		return nil, err
	}
	return kn, nil
}
