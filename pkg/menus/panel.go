// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"github.com/codelif/outputs"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/monitor"
)

// PanelOverrides is the one copy of the kitty tuning every menu panel
// (and historically each menu package) needs: pinned font size so cell
// metrics match the bar, no zoom/cursor noise, no dangerous paste.
//
// A panel with its own kitty process gets these as -o. Panels sharing one
// instance cannot, so the supervisor puts them on the instance instead.
var PanelOverrides = []string{
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

// panelConfig describes a warm menu-host panel on mon: hidden, pinned to
// that output (so its cell scale matches the eventual on-screen scale even
// while parked off it), parked past that output's right edge, with a
// non-grabbing focus policy so an idle spare never steals the keyboard.
// The host maps itself off-screen (paying the map round-trip while idle)
// and switches to exclusive focus only when a menu reveals it. Size is a
// placeholder — the host resizes to the real menu on open.
//
// A zero mon means the monitor is unknown; the panel then lands wherever
// the compositor puts it, parked far enough right to stay invisible.
func panelConfig(mon outputs.Monitor) katnip.Config {
	cfg := katnip.Config{
		Position:    katnip.Vector{X: 100000, Y: 0}, // fallback park when the output is unknown
		Size:        katnip.Vector{X: warmCols, Y: warmRows},
		Edge:        katnip.EdgeNone,
		Layer:       katnip.LayerTop,
		FocusPolicy: katnip.FocusNotAllowed,
	}
	if mon.Name != "" {
		cfg.OutputName = mon.Name
		if mon.ScaledWidth > 0 {
			cfg.Position.X = mon.ScaledWidth + offScreenMargin
		}
		// Spares are spawned by the supervisor, for every output rather
		// than its own, so the host is told which monitor it is on
		// instead of inheriting it.
		cfg.Env = monitor.WithOutput(nil, mon.Name)
	}
	return cfg
}

// spawnHostPanel starts a menu host in its own kitty process. Used when
// panels are not sharing an instance; see [Spawner] for the shared path.
func spawnHostPanel(mon outputs.Monitor) (*katnip.Panel, error) {
	cfg := panelConfig(mon)
	cfg.ConfigFile = "NONE"
	cfg.StartAsHidden = true
	cfg.KittyOverrides = PanelOverrides

	kn := katnip.NewPanel(hostInstance, cfg)
	if err := kn.Start(); err != nil {
		return nil, err
	}
	return kn, nil
}

// Spawner returns a spawn function that puts menu hosts in host's kitty
// instance instead of giving each one its own process. The instance already
// carries [PanelOverrides], so the panel needs none of its own.
//
// A shared panel cannot start hidden, since that is a property of the kitty
// process rather than the window. It does not need to: a spare is parked past
// its output's right edge, which is where the hidden ones map themselves
// anyway.
func Spawner(host *katnip.Host) func(outputs.Monitor) (*katnip.Panel, error) {
	return func(mon outputs.Monitor) (*katnip.Panel, error) {
		kn := host.NewPanel(hostInstance, panelConfig(mon))
		if err := kn.Start(); err != nil {
			return nil, err
		}
		return kn, nil
	}
}
