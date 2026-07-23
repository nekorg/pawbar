// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"testing"

	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// testGeo is a 1000x500-logical monitor at 2x with 10x20 physical
// pixels per cell: a 20-cell-wide parent panel is 104 logical units
// (20*10/2 + 2*panelPad).
func testGeo(panelX, panelY int) wire.Geometry {
	return wire.Geometry{
		MonW: 1000, MonH: 500,
		PanelX: panelX, PanelY: panelY,
		PPCX: 10, PPCY: 20,
		Scale: 2, Pad: panelPad,
	}
}

// Sizes below in terms of panelPad: a 20-cell parent is 100+2*panelPad
// logical wide, a 10x5-cell submenu is (50+2*panelPad)x(50+2*panelPad),
// and the overlap is one cell = 5 logical.
func TestPlaceSubmenuOpensRight(t *testing.T) {
	x, y, geo := placeSubmenu(testGeo(100, 50), 20, 3, 10, 5)
	if want := 100 + (100 + 2*panelPad) - 5; x != want {
		t.Errorf("x = %d, want %d", x, want)
	}
	// aligned with row 3: y = 50 + 3*20/2
	if y != 80 {
		t.Errorf("y = %d, want 80", y)
	}
	if geo.PanelX != x || geo.PanelY != y {
		t.Errorf("geometry (%d,%d) does not match position (%d,%d)", geo.PanelX, geo.PanelY, x, y)
	}
}

func TestPlaceSubmenuFlipsLeft(t *testing.T) {
	// Parent near the right edge: the submenu has no room right.
	x, _, _ := placeSubmenu(testGeo(900, 50), 20, 0, 10, 5)
	if want := 900 - (50 + 2*panelPad) + 5; x != want {
		t.Errorf("x = %d, want %d (flipped left)", x, want)
	}
}

func TestPlaceSubmenuClampsBottom(t *testing.T) {
	_, y, _ := placeSubmenu(testGeo(100, 480), 20, 0, 10, 5)
	if want := 500 - (50 + 2*panelPad); y != want {
		t.Errorf("y = %d, want %d (clamped to bottom)", y, want)
	}
}

func TestPlaceSubmenuUltraWideFallsBackToEdge(t *testing.T) {
	// Submenu wider than fits either side ends up flush left/right,
	// never negative.
	x, _, _ := placeSubmenu(testGeo(100, 50), 20, 0, 300, 5)
	if x != 0 {
		t.Errorf("x = %d, want 0", x)
	}
}

func TestPlaceSubmenuWithoutMonitorInfo(t *testing.T) {
	geo := testGeo(100, 50)
	geo.MonW, geo.MonH = 0, 0
	x, y, _ := placeSubmenu(geo, 20, 3, 10, 5)
	// No clamping possible; plain right-side placement.
	wantX := 100 + (100 + 2*panelPad) - 5
	if x != wantX || y != 80 {
		t.Errorf("(x,y) = (%d,%d), want (%d,80)", x, y, wantX)
	}
}

func TestClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-3, 0, 10, 0},
		{15, 0, 10, 10},
		{5, 0, -2, 0}, // inverted range collapses to lo
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestCellsToLogical(t *testing.T) {
	// 20 cells at 10 physical px/cell on a 2x display: 100 logical
	// plus panel chrome on both edges.
	if got := cellsToLogical(20, 10, 2); got != 100+2*panelPad {
		t.Errorf("cellsToLogical = %d, want %d", got, 100+2*panelPad)
	}
	// Fractional results round up so clamping errs on-screen.
	if got := cellsToLogical(3, 10, 4); got != 8+2*panelPad {
		t.Errorf("cellsToLogical = %d, want %d", got, 8+2*panelPad)
	}
}
