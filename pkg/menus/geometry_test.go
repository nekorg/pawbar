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

// Sizes below: a 20-cell parent is exactly 100 logical wide and a
// 10x5-cell submenu exactly 50x50; panelPad only widens the edge checks.
func TestPlaceSubmenuOpensRight(t *testing.T) {
	x, y, geo := placeSubmenu(testGeo(100, 50), 20, 3, 10, 5)
	// flush with the parent's real right edge
	if want := 100 + 100; x != want {
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
	if want := 900 - 50; x != want {
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
	wantX := 100 + 100
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

func TestPlaceSubmenuRecordsWhatItWasPlacedAgainst(t *testing.T) {
	_, _, geo := placeSubmenu(testGeo(100, 50), 20, 3, 10, 5)
	if !geo.IsSub() {
		t.Fatal("submenu geometry does not read as a submenu")
	}
	if geo.ParentX != 100 || geo.ParentY != 50 || geo.ParentW != 20 || geo.Row != 3 {
		t.Errorf("parent = (%d,%d) w=%d row=%d, want (100,50) w=20 row=3",
			geo.ParentX, geo.ParentY, geo.ParentW, geo.Row)
	}
	if geo.AnchorX != 0 || geo.AnchorY != 0 {
		t.Error("a submenu kept its parent's root anchor")
	}
}

// placedSession is a panel already positioned at geo, with no measured
// metrics of its own so geo's are used.
func placedSession(geo wire.Geometry) *Session { return &Session{geo: geo} }

func TestPlaceKeepsAFlippedSubmenuFlushOnItsRightEdge(t *testing.T) {
	// Parent at the right edge, so the submenu flipped to its left: the
	// edge that has to stay put is the right one.
	_, _, geo := placeSubmenu(testGeo(900, 50), 20, 0, 10, 5)
	s := placedSession(geo)

	// Shrink to 6 cells (30 logical). Merely clamping would leave x where
	// the 10-cell panel put it and let the right edge walk left.
	x, _, _, moved := s.place(6, 5)
	if !moved {
		t.Fatal("shrinking a flipped submenu did not move it")
	}
	if want := 900 - 30; x != want {
		t.Errorf("x = %d, want %d (right edge still flush with the parent)", x, want)
	}
}

func TestPlaceKeepsASubmenuFlushOnItsLeftEdge(t *testing.T) {
	_, _, geo := placeSubmenu(testGeo(100, 50), 20, 3, 10, 5)
	s := placedSession(geo)
	x, y, _, moved := s.place(30, 5)
	if moved {
		t.Error("widening a right-side submenu moved it; its left edge is the flush one")
	}
	if want := 100 + 100; x != want {
		t.Errorf("x = %d, want %d", x, want)
	}
	if y != 80 {
		t.Errorf("y = %d, want 80", y)
	}
}

func TestPlaceReflipsASubmenuThatGrewPastTheEdge(t *testing.T) {
	_, _, geo := placeSubmenu(testGeo(700, 50), 20, 0, 10, 5)
	s := placedSession(geo)
	// 60 cells is 300 logical: no longer fits to the right of 795.
	x, _, _, moved := s.place(60, 5)
	if !moved {
		t.Fatal("a submenu that outgrew the right side was not re-placed")
	}
	if want := 700 - 300; x != want {
		t.Errorf("x = %d, want %d (flipped left)", x, want)
	}
}

func TestPlaceReturnsARootTowardItsAnchorOnShrink(t *testing.T) {
	// A 20-cell root (100 logical + 2*panelPad) anchored at 950 is clamped
	// left to fit.
	geo := testGeo(0, 40)
	geo.AnchorX, geo.AnchorY = 950, 40
	geo.PanelX, geo.PanelY = clampAt(950, 40, 20, 5, geo)
	if want := 1000 - (100 + 2*panelPad); geo.PanelX != want {
		t.Fatalf("setup: PanelX = %d, want %d", geo.PanelX, want)
	}

	// Now it shrinks to 10 cells and fits again: it belongs back at its
	// anchor, not parked where the wider content pushed it.
	s := placedSession(geo)
	x, _, _, moved := s.place(10, 5)
	if !moved {
		t.Fatal("a root that shrank was not re-placed")
	}
	if want := 1000 - (50 + 2*panelPad); x != want {
		t.Errorf("x = %d, want %d", x, want)
	}
	if x <= geo.PanelX {
		t.Error("shrinking left the menu pinned where its widest content forced it")
	}
}

func TestPlaceWithoutMonitorInfoStaysPut(t *testing.T) {
	geo := testGeo(100, 50)
	geo.MonW, geo.MonH = 0, 0
	x, y, _, moved := placedSession(geo).place(30, 9)
	if moved || x != 100 || y != 50 {
		t.Errorf("place = (%d,%d) moved=%v, want (100,50) false", x, y, moved)
	}
}

// measuredSession is a panel at geo that measures its own surface as
// cols x rows of ppc pixels — slightly off the parent's, as a narrower
// panel's rounding makes it.
func measuredSession(geo wire.Geometry, cols, rows int, ppcX, ppcY float64) *Session {
	s := &Session{geo: geo}
	s.setSize(cols, rows, int(float64(cols)*ppcX), int(float64(rows)*ppcY))
	return s
}

func TestPlaceLeavesAnUnchangedSubmenuWhereTheBarPutIt(t *testing.T) {
	// The bar places a 12-cell submenu against a 30-cell parent.
	const parentCells, subCells, subRows = 30, 12, 5
	barX, barY, geo := placeSubmenu(testGeo(100, 50), parentCells, 2, subCells, subRows)

	// The submenu re-places itself at the same size, having measured its
	// own surface a shade under the parent's 10px cells.
	s := measuredSession(geo, subCells, subRows, 9.8, 19.6)
	x, y, _, moved := s.place(subCells, subRows)

	if moved {
		t.Errorf("a submenu re-placed at its own size moved to (%d,%d) from (%d,%d)", x, y, barX, barY)
	}
	if x != barX || y != barY {
		t.Errorf("place = (%d,%d), want the bar's own (%d,%d)", x, y, barX, barY)
	}
	// Specifically: still flush against the parent's edge.
	parentRight := 100 + cellsToLogical(parentCells, 10, 2)
	if x != parentRight {
		t.Errorf("x = %d, want %d (flush with the parent's edge)", x, parentRight)
	}
}

func TestPlaceUsesTheParentsMetricsForTheParentsWidth(t *testing.T) {
	// Same as above but the panel's own measurement is far off, so folding
	// it into the parent-sized span would be unmissable.
	const parentCells, subCells, subRows = 40, 10, 5
	barX, _, geo := placeSubmenu(testGeo(100, 50), parentCells, 0, subCells, subRows)

	s := measuredSession(geo, subCells, subRows, 8, 16)
	x, _, _, _ := s.place(subCells, subRows)
	if x != barX {
		t.Errorf("x = %d, want %d: the parent's width must be measured in the parent's cells", x, barX)
	}
}

func TestPlaceStillUsesOwnMetricsForARoot(t *testing.T) {
	// A root's clamp measures only its own box, so its own metrics win.
	geo := testGeo(0, 40)
	geo.AnchorX, geo.AnchorY = 950, 40
	geo.PanelX, geo.PanelY = clampAt(950, 40, 20, 5, geo)

	s := measuredSession(geo, 20, 5, 12, 24)
	x, _, _, _ := s.place(20, 5)
	// 20 cells at 12px on 2x is 120 logical, plus 2*panelPad.
	if want := 1000 - (120 + 2*panelPad); x != want {
		t.Errorf("x = %d, want %d (clamped with the panel's own metrics)", x, want)
	}
}
