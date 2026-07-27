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
)

// layout state is package-global, so these tests reseed it and cannot run
// in parallel.

func cellsText(cells []cell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(c.c.Character.Grapheme)
	}
	return b.String()
}

func runsText(runs []run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(cellsText(r.cells))
	}
	return b.String()
}

// setupFit puts segs in one right-anchored slot at a given bar width, the
// shape of the config that motivated all this. Text is ASCII so the column
// arithmetic in the expectations is exact.
func setupFit(t *testing.T, w, shrinkMin int, segs []module.Segment) []run {
	t.Helper()
	Init(w, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		ShrinkMin:        shrinkMin,
	})
	SetSlotCounts(0, 0, 1)
	SetSpacerSlots(nil, nil, []bool{false})
	SetSnapshot(2, 0, segs)

	return fit()[2]
}

// The reported bug: a right-anchored mpris used to lose its play/pause icon
// first, because the block was trimmed from its head. Rigid pieces must now
// survive untouched while the elastic ones give way.
func TestFitKeepsRigidPiecesIntact(t *testing.T) {
	runs := setupFit(t, 20, 3, []module.Segment{
		{Text: ">"},                       // icon: 1 rigid
		{Text: "TITLETITLE", Shrink: 1},   // 10 elastic
		{Text: " * "},                     // 3 rigid
		{Text: "ARTISTARTIST", Shrink: 1}, // 12 elastic
	})

	// 26 columns of content into 20: the 22 elastic columns are cut to 16,
	// levelled at 8 each; the icon and the separator never move.
	const want = ">TITLETI… * ARTISTA…"
	if got := runsText(runs); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if w := runsWidth(runs); w != 20 {
		t.Errorf("laid out %d columns, want 20", w)
	}
}

func TestFitMaxMinFairness(t *testing.T) {
	cases := []struct {
		name  string
		width int
		segs  []module.Segment
		want  string
	}{
		{
			// The long one absorbs the whole cut until it draws level
			// with the short one, which is never touched.
			name:  "longest gives way first",
			width: 8,
			segs: []module.Segment{
				{Text: "AAAAAAAAAA", Shrink: 1},
				{Text: "BBB", Shrink: 1},
			},
			want: "AAAA…BBB",
		},
		{
			// Once level, they shrink together.
			name:  "equal pieces shrink equally",
			width: 12,
			segs: []module.Segment{
				{Text: "AAAAAAAAAA", Shrink: 1},
				{Text: "BBBBBBBBBB", Shrink: 1},
			},
			want: "AAAAA…BBBBB…",
		},
		{
			// Weight 2 keeps twice the columns of weight 1.
			name:  "weights split the budget",
			width: 12,
			segs: []module.Segment{
				{Text: "AAAAAAAAAAAAAAAAAAAA", Shrink: 2},
				{Text: "BBBBBBBBBBBBBBBBBBBB", Shrink: 1},
			},
			want: "AAAAAAA…BBB…",
		},
		{
			name:  "nothing to do when it already fits",
			width: 40,
			segs: []module.Segment{
				{Text: "AAAAAAAAAA", Shrink: 1},
				{Text: "BBB"},
			},
			want: "AAAAAAAAAABBB",
		},
		{
			// No elastic pieces: shrinking has nothing to work with and
			// Render's positional trim is left to deal with it.
			name:  "all rigid is left alone",
			width: 4,
			segs: []module.Segment{
				{Text: "AAAAAAAAAA"},
			},
			want: "AAAAAAAAAA",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runsText(setupFit(t, c.width, 3, c.segs)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestFitRespectsShrinkMin(t *testing.T) {
	// 20 columns of elastic text into 6, floored at 3 each: the floor wins
	// and the row stays wider than the bar for the positional trim.
	runs := setupFit(t, 6, 3, []module.Segment{
		{Text: "AAAAAAAAAA", Shrink: 1},
		{Text: "BBBBBBBBBB", Shrink: 1},
	})
	if got, want := runsText(runs), "AA…BB…"; got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// Even asked for less than the floors add up to, no piece goes below it.
	runs = setupFit(t, 4, 3, []module.Segment{
		{Text: "AAAAAAAAAA", Shrink: 1},
		{Text: "BBBBBBBBBB", Shrink: 1},
	})
	for i, r := range runs {
		if w := totalWidth(r.cells); w < 3 {
			t.Errorf("run %d shrank to %d columns, below the floor of 3", i, w)
		}
	}
}

// Two equally fair answers must not alternate between frames, or the bar
// visibly jitters at a fixed width.
func TestFitIsStable(t *testing.T) {
	segs := []module.Segment{
		{Text: "AAAAAAAAA", Shrink: 1},
		{Text: "BBBBBBBB", Shrink: 1},
		{Text: "CCCCCCC", Shrink: 1},
	}
	first := runsText(setupFit(t, 15, 3, segs))
	for range 5 {
		if got := runsText(setupFit(t, 15, 3, segs)); got != first {
			t.Fatalf("layout changed between identical frames: %q then %q", first, got)
		}
	}
}

// setupSides lays out all three anchors at once: each entry is one slot's
// segments, and order is the bar.truncate_priority to place them in.
func setupSides(t *testing.T, w int, order []string, l, m, r []module.Segment) [3][]run {
	t.Helper()
	Init(w, 1, config.BarSettings{
		TruncatePriority: order,
		Ellipsis:         "…",
		ShrinkMin:        3,
	})
	SetSlotCounts(1, 1, 1)
	SetSpacerSlots([]bool{false}, []bool{false}, []bool{false})
	for side, segs := range [3][]module.Segment{l, m, r} {
		SetSnapshot(side, 0, segs)
	}
	return fit()
}

// A centered middle module halves the bar: the right side can only use the
// columns past it, however empty the left half is. Counting columns
// bar-wide says this row fits — 2 + 5 + 31 into 60 — so nothing would
// shrink, and Render would then trim the right block head-first and eat the
// icon. The room a side really has is what it must be fitted against.
func TestFitAccountsForCenteredMiddle(t *testing.T) {
	runs := setupSides(t, 60, []string{"middle", "right", "left"},
		[]module.Segment{{Text: "WS"}},
		[]module.Segment{{Text: "CLOCK"}},
		[]module.Segment{
			{Text: ">"},
			{Text: "TITLETITLETITLE", Shrink: 1},
			{Text: " * "},
			{Text: "ARTISTARTIST", Shrink: 1},
		})

	// CLOCK is centered over columns 27..31, leaving the right side 28.
	const want = ">TITLETITLET… * ARTISTARTIST"
	if got := runsText(runs[2]); got != want {
		t.Errorf("right: got %q want %q", got, want)
	}
	if w := runsWidth(runs[2]); w > 28 {
		t.Errorf("right side is %d columns, past the %d it can draw into", w, 28)
	}
	if got := runsText(runs[0]); got != "WS" {
		t.Errorf("left had room and should be untouched: got %q", got)
	}
}

// Sides are fitted in bar.truncate_priority order, which is what that
// setting has always promised: the anchor listed first keeps its content,
// and the ones after it live with what is left.
func TestFitFollowsTruncatePriority(t *testing.T) {
	long := []module.Segment{{Text: "AAAAAAAAAAAAAAAAAAAA", Shrink: 1}} // 20
	short := []module.Segment{{Text: "BBBBBBBBBB", Shrink: 1}}          // 10

	// 30 columns of elastic text into 24, no middle to split the bar.
	runs := setupSides(t, 24, []string{"right", "left", "middle"}, short, nil, long)
	if got := runsText(runs[2]); got != "AAAAAAAAAAAAAAAAAAAA" {
		t.Errorf("right is first in priority and fits alone: got %q", got)
	}
	if got := runsText(runs[0]); got != "BBB…" {
		t.Errorf("left absorbs the deficit: got %q want %q", got, "BBB…")
	}

	// Flipping the priority flips who gives way.
	runs = setupSides(t, 24, []string{"left", "middle", "right"}, short, nil, long)
	if got := runsText(runs[0]); got != "BBBBBBBBBB" {
		t.Errorf("left is first in priority now: got %q", got)
	}
	if got := runsText(runs[2]); got != "AAAAAAAAAAAAA…" {
		t.Errorf("right absorbs the deficit: got %q", got)
	}
}

// The whole chain, as a user configures it: a format string with `~`
// compiled by the module package, rendered through a real Writer, laid out
// by the real fitting pass.
func TestEndToEndElasticFormat(t *testing.T) {
	f := module.MustFormat("{icon} {title~} * {artists~}")
	w := module.NewWriter(func([]string) module.Resolved {
		return module.Resolved{Formatter: f}
	})
	w.Text(module.P{
		"icon":    ">",
		"title":   "TITLETITLE",
		"artists": "ARTISTARTIST",
	})
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}

	Init(21, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		ShrinkMin:        3,
	})
	SetSlotCounts(0, 0, 1)
	SetSpacerSlots(nil, nil, []bool{false})
	SetSnapshot(2, 0, w.Segments())

	// 27 columns into 21: the icon and the " * " are untouchable, and the
	// two elastic pieces level off at 8 each.
	if got, want := runsText(fit()[2]), "> TITLETI… * ARTISTA…"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
