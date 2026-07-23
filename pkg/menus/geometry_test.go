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

// Sizes below: a 20-cell parent is exactly 100 logical wide, a
// 10x5-cell submenu is exactly 50x50, the overlap is one cell = 5
// logical; panelPad only widens the edge checks.
func TestPlaceSubmenuOpensRight(t *testing.T) {
	x, y, geo := placeSubmenu(testGeo(100, 50), 20, 3, 10, 5)
	// flush with the parent's real right edge minus the overlap
	if want := 100 + 100 - 5; x != want {
		t.Errorf("x = %d, want %d", x, want)
	}
	// exactly level with row 3: y = round((50*2 + 3*20)/2)
	if y != 80 {
		t.Errorf("y = %d, want 80", y)
	}
	if geo.PanelX != x || geo.PanelY != y {
		t.Errorf("geometry (%d,%d) does not match position (%d,%d)", geo.PanelX, geo.PanelY, x, y)
	}
}

func TestPlaceSubmenuRowAlignmentRounds(t *testing.T) {
	// Fractional logical row offsets round to the nearest unit instead
	// of truncating: row 3 at 21 physical px/cell on 2x is 31.5.
	geo := testGeo(100, 50)
	geo.PPCY = 21
	_, y, _ := placeSubmenu(geo, 20, 3, 10, 5)
	// y = round((50*2 + 3*21)/2) = round(81.5) = 82
	if y != 82 {
		t.Errorf("y = %d, want 82 (rounded)", y)
	}
}

func TestPlaceSubmenuFlipsLeft(t *testing.T) {
	// Parent near the right edge: the submenu has no room right.
	x, _, _ := placeSubmenu(testGeo(900, 50), 20, 0, 10, 5)
	if want := 900 - 50 + 5; x != want {
		t.Errorf("x = %d, want %d (flipped left)", x, want)
	}
}

func TestPlaceSubmenuClampsBottom(t *testing.T) {
	_, y, _ := placeSubmenu(testGeo(100, 480), 20, 0, 10, 5)
	if want := 500 - 50 - panelPad; y != want {
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
	wantX := 100 + 100 - 5
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
	// 20 cells at 10 physical px/cell on a 2x display: exactly 100
	// logical units.
	if got := cellsToLogical(20, 10, 2); got != 100 {
		t.Errorf("cellsToLogical = %d, want %d", got, 100)
	}
	// Fractional results round up so sizes err on the large side.
	if got := cellsToLogical(3, 10, 4); got != 8 {
		t.Errorf("cellsToLogical = %d, want %d", got, 8)
	}
}
