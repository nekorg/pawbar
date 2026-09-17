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
	Init(cols, 1, config.BarSettings{
		TruncatePriority: order,
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(1, 1, 1)
	SetSpacerSlots([]bool{false}, []bool{false}, []bool{false})
	SetSlotPriorities([]int{0}, []int{0}, []int{0})
	for side, text := range []string{left, mid, right} {
		SetSnapshot(side, 0, [][]module.Segment{{{Text: text}}})
	}
	Render(win)

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
	const cols = 20
	win := vaxis.NewOffscreenWindow(cols, 1)

	ellipsis := true
	Init(cols, 1, config.BarSettings{
		TruncatePriority: []string{"left", "middle", "right"},
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(1, 0, 0)
	SetSpacerSlots([]bool{false}, nil, nil)
	SetSlotPriorities([]int{0}, nil, nil)

	read := func() string {
		Render(win)
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

	SetSnapshot(0, 0, [][]module.Segment{{{Text: "before"}}})
	if got := read(); got != "before" {
		t.Fatalf("first frame = %q, want %q", got, "before")
	}

	SetSnapshot(0, 0, [][]module.Segment{{{Text: "after"}}})
	if got := read(); got != "after" {
		t.Fatalf("second frame = %q, want %q; cached cells went stale", got, "after")
	}
}

// Stepping a slot down its ladder and back up must not serve the narrow
// level's cells at the wide level.
func TestLevelsAreCachedApart(t *testing.T) {
	const wide = 40
	win := vaxis.NewOffscreenWindow(wide, 1)

	ellipsis := true
	Init(wide, 1, config.BarSettings{
		TruncatePriority: []string{"left", "middle", "right"},
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(1, 0, 0)
	SetSpacerSlots([]bool{false}, nil, nil)
	SetSlotPriorities([]int{0}, nil, nil)
	SetSnapshot(0, 0, [][]module.Segment{
		{{Text: "a-very-long-label"}},
		{{Text: "short"}},
	})

	read := func(cols int) string {
		Render(win)
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
	Resize(8, 1)
	if got := read(8); got != "short" {
		t.Fatalf("narrow = %q, want the stepped-down level", got)
	}
	Resize(wide, 1)
	if got := read(wide); got != "a-very-long-label" {
		t.Fatalf("widened back = %q, want the wide level again", got)
	}
}
