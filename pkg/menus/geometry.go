// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"math"
	"sync"

	"github.com/codelif/outputs"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/nekorg/pawbar/internal/scale"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// Anchor is where a menu should open: the invoking click's position in
// physical pixels, as delivered by SGR-pixels mouse events on the bar.
type Anchor struct {
	XPixel, YPixel int
}

// panelPad approximates the panel chrome (kitty padding/border plus
// margin rounding) in logical units per edge, so clamping errs on
// staying on-screen. Deliberately generous: the only cost is a menu
// clamped a few extra pixels away from the edge.
const panelPad = 6

var metrics struct {
	mu         sync.Mutex
	ppcX, ppcY float64
}

// SetCellMetrics records the bar's physical pixels-per-cell, taken from
// its vaxis size report. Menus pin the same kitty font size, so their
// cell metrics match the bar's on the same output.
func SetCellMetrics(cols, rows, xPixel, yPixel int) {
	if cols <= 0 || rows <= 0 || xPixel <= 0 || yPixel <= 0 {
		return
	}
	metrics.mu.Lock()
	metrics.ppcX = float64(xPixel) / float64(cols)
	metrics.ppcY = float64(yPixel) / float64(rows)
	metrics.mu.Unlock()
}

// cellMetrics returns physical pixels per cell, falling back to an
// estimate for the pinned 12pt font when the bar never reported (e.g.
// the standalone dbusmenu binary).
func cellMetrics() (float64, float64) {
	metrics.mu.Lock()
	x, y := metrics.ppcX, metrics.ppcY
	metrics.mu.Unlock()
	if x > 0 && y > 0 {
		return x, y
	}
	f := scale.Factor()
	logging.Log.Warn().Msg("menus: no cell metrics reported; estimating from font size")
	return 8 * f, 17 * f
}

// output returns the monitor menus are placed on: the one this bar runs
// on, so a menu opens on the screen it was clicked from and is clamped to
// that screen's geometry.
func output() (outputs.Monitor, bool) { return monitor.Info() }

// cellsToLogical converts a size in cells to compositor-logical units.
// This is the panel's real footprint; clamping adds panelPad on top,
// placement math must not.
func cellsToLogical(cells int, ppc, f float64) int {
	return int(math.Ceil(float64(cells) * ppc / f))
}

// clampRoot converts a click anchor to a clamped logical position for a
// menu of wCells x hCells, and packages the geometry the child needs for
// re-clamping after live resizes.
func clampRoot(at Anchor, wCells, hCells int) (int, int, wire.Geometry) {
	f := scale.Factor()
	x, y := scale.Logical(at.XPixel, at.YPixel)
	ppcX, ppcY := cellMetrics()

	geo := wire.Geometry{PPCX: ppcX, PPCY: ppcY, Scale: f, Pad: panelPad}
	mon, ok := output()
	if ok {
		geo.MonW, geo.MonH = mon.ScaledWidth, mon.ScaledHeight
		w := cellsToLogical(wCells, ppcX, f) + 2*panelPad
		h := cellsToLogical(hCells, ppcY, f) + 2*panelPad
		x = clamp(x, 0, mon.ScaledWidth-w)
		y = clamp(y, 0, mon.ScaledHeight-h)
	}
	geo.PanelX, geo.PanelY = x, y
	return x, y, geo
}

// placeSubmenu positions a submenu of subW x subH cells next to its
// parent panel, its first row exactly level with the hovered row. It
// opens to the right with a one-cell overlap and flips to the left
// edge of the parent when there is no room.
func placeSubmenu(parent wire.Geometry, parentWCells, row, subWCells, subHCells int) (int, int, wire.Geometry) {
	f := parent.Scale
	overlap := int(parent.PPCX / f)
	pW := cellsToLogical(parentWCells, parent.PPCX, f)
	sW := cellsToLogical(subWCells, parent.PPCX, f)
	sH := cellsToLogical(subHCells, parent.PPCY, f)

	x := parent.PanelX + pW - overlap
	// Compute the row's position in physical pixels and round once, so
	// the only remaining error is the compositor's logical-pixel
	// granularity.
	y := int(math.Round((float64(parent.PanelY)*f + float64(row)*parent.PPCY) / f))

	geo := parent
	if parent.MonW > 0 {
		if x+sW+parent.Pad > parent.MonW {
			x = parent.PanelX - sW + overlap
			if x < 0 {
				x = max(0, parent.MonW-sW-parent.Pad)
			}
		}
		y = clamp(y, 0, parent.MonH-sH-parent.Pad)
	}
	geo.PanelX, geo.PanelY = x, y
	return x, y, geo
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
