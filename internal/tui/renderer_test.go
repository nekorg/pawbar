// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"strings"
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// drawBar lays three sides out into an offscreen window and returns the row
// as a string, which is what the terminal would have shown.
func drawBar(t *testing.T, cols int, order []string, left, mid, right string) string {
	t.Helper()
	win := vaxis.NewOffscreenWindow(cols, 1)

	ellipsis := true
	lay := New(win.Vx, cols, config.BarSettings{
		TruncatePriority: order,
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	lay.SetSlotCounts(1, 1, 1)
	lay.SetSpacerSlots([]bool{false}, []bool{false}, []bool{false})
	lay.SetSlotPriorities([]int{0}, []int{0}, []int{0})
	for side, text := range []string{left, mid, right} {
		lay.SetSnapshot(side, 0, [][]module.Segment{{{Content: module.Text{S: text}}}})
	}
	lay.Render(win)

	var b strings.Builder
	for col := range cols {
		g := win.Vx.Cell(col, 0).Grapheme
		if g == "" {
			g = " "
		}
		b.WriteString(g)
	}
	return b.String()
}

// A middle block belongs in the middle. It used to go hunting for free columns
// anywhere in the bar once its own span was covered, which dropped the clock
// next to the tray and spent room the fitting pass had already promised the
// right side.
func TestMiddleStaysInTheMiddle(t *testing.T) {
	t.Parallel()
	const cols = 40
	const order = "left,middle,right"

	tests := []struct {
		name string
		left string
	}{
		{"clear of the middle", "aaaa"},
		{"over the middle's left half", strings.Repeat("a", 20)},
		{"covering the middle entirely", strings.Repeat("a", 30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := drawBar(t, cols, strings.Split(order, ","), tt.left, "CLOCK", "rrrrrrr")

			// The right side keeps its columns in every case: nothing may be
			// drawn past where the middle's own span ends.
			if !strings.HasSuffix(strings.TrimRight(got, " "), "rrrrrrr") {
				t.Errorf("right side was eaten: %q", got)
			}
			if i := strings.Index(got, "CLOCK"); i >= 0 {
				mid := i + len("CLOCK")/2
				if mid < cols/2-3 || mid > cols/2+3 {
					t.Errorf("clock centred on column %d of %d: %q", mid, cols, got)
				}
			}
			t.Logf("%q", got)
		})
	}
}

// With the middle first in the truncate order it keeps its columns outright
// and the left is the side that gives way, which is the setting to reach for
// when the clock must always be there.
func TestMiddleFirstKeepsItsColumns(t *testing.T) {
	t.Parallel()
	const cols = 40
	got := drawBar(t, cols, []string{"middle", "left", "right"},
		strings.Repeat("a", 30), "CLOCK", "rrrrrrr")
	if !strings.Contains(got, "CLOCK") {
		t.Errorf("clock gone: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("left side was not trimmed against the clock: %q", got)
	}
	t.Logf("%q", got)
}

// Laid-out cells are cached per slot and detail level. A new snapshot for a
// slot has to drop that slot's entry, or the bar keeps drawing the old text
// forever.
func TestSnapshotInvalidatesCachedCells(t *testing.T) {
	t.Parallel()
	const cols = 20
	win := vaxis.NewOffscreenWindow(cols, 1)

	ellipsis := true
	lay := New(win.Vx, cols, config.BarSettings{
		TruncatePriority: []string{"left", "middle", "right"},
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	lay.SetSlotCounts(1, 0, 0)
	lay.SetSpacerSlots([]bool{false}, nil, nil)
	lay.SetSlotPriorities([]int{0}, nil, nil)

	read := func() string {
		lay.Render(win)
		var b strings.Builder
		for col := range cols {
			g := win.Vx.Cell(col, 0).Grapheme
			if g == "" {
				g = " "
			}
			b.WriteString(g)
		}
		return strings.TrimRight(b.String(), " ")
	}

	lay.SetSnapshot(0, 0, [][]module.Segment{{{Content: module.Text{S: "before"}}}})
	if got := read(); got != "before" {
		t.Fatalf("first frame = %q, want %q", got, "before")
	}

	lay.SetSnapshot(0, 0, [][]module.Segment{{{Content: module.Text{S: "after"}}}})
	if got := read(); got != "after" {
		t.Fatalf("second frame = %q, want %q; cached cells went stale", got, "after")
	}
}

// Stepping a slot down its ladder and back up must not serve the narrow
// level's cells at the wide level.
func TestLevelsAreCachedApart(t *testing.T) {
	t.Parallel()
	const wide = 40
	win := vaxis.NewOffscreenWindow(wide, 1)

	ellipsis := true
	lay := New(win.Vx, wide, config.BarSettings{
		TruncatePriority: []string{"left", "middle", "right"},
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	lay.SetSlotCounts(1, 0, 0)
	lay.SetSpacerSlots([]bool{false}, nil, nil)
	lay.SetSlotPriorities([]int{0}, nil, nil)
	lay.SetSnapshot(0, 0, [][]module.Segment{
		{{Content: module.Text{S: "a-very-long-label"}}},
		{{Content: module.Text{S: "short"}}},
	})

	read := func(cols int) string {
		lay.Render(win)
		var b strings.Builder
		for col := range cols {
			g := win.Vx.Cell(col, 0).Grapheme
			if g == "" {
				g = " "
			}
			b.WriteString(g)
		}
		return strings.TrimRight(b.String(), " ")
	}

	if got := read(wide); got != "a-very-long-label" {
		t.Fatalf("wide = %q", got)
	}
	lay.Resize(8)
	if got := read(8); got != "short" {
		t.Fatalf("narrow = %q, want the stepped-down level", got)
	}
	lay.Resize(wide)
	if got := read(wide); got != "a-very-long-label" {
		t.Fatalf("widened back = %q, want the wide level again", got)
	}
}

// The point of splitting layout from paint: what the bar decided to show is
// settled before any window exists, so it can be read back with no terminal,
// no font and no compositor in the way.
func TestLayoutFillsTheRowWithoutAWindow(t *testing.T) {
	t.Parallel()
	lay := New(nil, 20, config.BarSettings{
		TruncatePriority: []string{"left", "middle", "right"},
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	lay.SetSlotCounts(1, 0, 1)
	lay.SetSpacerSlots([]bool{false}, nil, []bool{false})
	lay.SetSlotPriorities([]int{0}, nil, []int{0})
	lay.SetSnapshot(0, 0, [][]module.Segment{{module.Txt("cpu")}})
	lay.SetSnapshot(2, 0, [][]module.Segment{{module.Txt("12:00")}})

	lay.layout()

	row := ""
	for col := range 20 {
		row += lay.state[col].c.Grapheme
	}
	if want := "cpu            12:00"; row != want {
		t.Errorf("row = %q, want %q", row, want)
	}

	// And the hit table came with it: the left slot answers for its own
	// columns, the right slot for its own, the gap between for neither.
	if hit, ok := lay.HitAt(0, true); !ok || hit.Side != 0 || hit.Index != 0 {
		t.Errorf("column 0: got %+v ok=%v, want left slot 0", hit, ok)
	}
	if hit, ok := lay.HitAt(19, false); !ok || hit.Side != 2 || hit.Index != 0 {
		t.Errorf("column 19: got %+v ok=%v, want right slot 0", hit, ok)
	}
	if _, ok := lay.HitAt(8, true); ok {
		t.Error("empty column claimed a module")
	}
}
