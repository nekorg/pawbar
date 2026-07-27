// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"slices"
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// setupFit puts segs in one right-anchored slot at a given bar width, the
// shape of the config that motivated all this. Text is ASCII so the column
// arithmetic in the expectations is exact.
func setupFit(t *testing.T, w, shrinkMin int, segs []module.Segment) []run {
	t.Helper()
	Init(w, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		ShrinkMin:        shrinkMin,
	}, vaxis.Style{})
	SetSlotCounts(0, 0, 1)
	SetSpacerSlots(nil, nil, []bool{false})
	SetSlotPriorities(nil, nil, []int{0})
	SetSnapshot(2, 0, [][]module.Segment{segs})

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

// setupLadder puts one slot per side entry at a given width, each with its
// own detail levels and priority, and returns the laid-out text per side.
func setupLadder(t *testing.T, w int, prios []int, slots [][][]module.Segment) []string {
	t.Helper()
	Init(w, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(len(slots), 0, 0)
	SetSpacerSlots(make([]bool, len(slots)), nil, nil)
	SetSlotPriorities(prios, nil, nil)
	for i, s := range slots {
		SetSnapshot(0, i, s)
	}

	fit()
	out := make([]string, len(slots))
	for i := range slots {
		out[i] = segsText(slotSegments(0, i))
	}
	return out
}

func segsText(segs []module.Segment) string {
	s := ""
	for _, seg := range segs {
		s += seg.Text
	}
	return s
}

// rungs builds a slot's ladder from plain rigid strings, widest first.
func rungs(texts ...string) [][]module.Segment {
	out := make([][]module.Segment, len(texts))
	for i, t := range texts {
		out[i] = []module.Segment{{Text: t}}
	}
	return out
}

func TestFitStepsDownLadder(t *testing.T) {
	// Two modules, each able to drop from 8 columns to 3.
	slots := [][][]module.Segment{
		rungs("AAAAAAAA", "AAA"),
		rungs("BBBBBBBB", "BBB"),
	}

	cases := []struct {
		name  string
		width int
		prios []int
		want  []string
	}{
		{
			name:  "room for everything",
			width: 20,
			prios: []int{0, 0},
			want:  []string{"AAAAAAAA", "BBBBBBBB"},
		},
		{
			// Only one needs to give way; the innermost goes first.
			name:  "one step is enough",
			width: 12,
			prios: []int{0, 0},
			want:  []string{"AAAAAAAA", "BBB"},
		},
		{
			name:  "both step down",
			width: 6,
			prios: []int{0, 0},
			want:  []string{"AAA", "BBB"},
		},
		{
			// Priority overrides position: the lower one degrades first
			// even though it sits further out.
			name:  "priority decides who gives way",
			width: 12,
			prios: []int{-1, 0},
			want:  []string{"AAA", "BBBBBBBB"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := setupLadder(t, c.width, c.prios, slots)
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

// Levels are recomputed each frame, so a bar that widens back out must
// recover the detail it gave up.
func TestFitLadderRecoversOnWidening(t *testing.T) {
	slots := [][][]module.Segment{rungs("AAAAAAAA", "AAA")}

	if got := setupLadder(t, 4, []int{0}, slots); got[0] != "AAA" {
		t.Fatalf("narrow: got %q want %q", got[0], "AAA")
	}
	if got := setupLadder(t, 40, []int{0}, slots); got[0] != "AAAAAAAA" {
		t.Errorf("widened again: got %q want %q", got[0], "AAAAAAAA")
	}
}

// Shrinking comes first: a module with both elastic text and a ladder
// shortens its text before it drops structure.
func TestFitShrinksBeforeSteppingDown(t *testing.T) {
	Init(8, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(1, 0, 0)
	SetSpacerSlots([]bool{false}, nil, nil)
	SetSlotPriorities([]int{0}, nil, nil)
	SetSnapshot(0, 0, [][]module.Segment{
		{{Text: ">"}, {Text: "TITLETITLE", Shrink: 1}}, // 11 columns
		{{Text: ">"}}, // the compact rung
	})

	// 11 into 8: the elastic title alone can close the gap, so the rung
	// with no title at all must not be reached.
	if got, want := runsText(fit()[0]), ">TITLET…"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if levels[0][0] != 0 {
		t.Errorf("stepped down to level %d when shrinking was enough", levels[0][0])
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
	}, vaxis.Style{})
	SetSlotCounts(1, 1, 1)
	SetSpacerSlots([]bool{false}, []bool{false}, []bool{false})
	SetSlotPriorities([]int{0}, []int{0}, []int{0})
	for side, segs := range [3][]module.Segment{l, m, r} {
		SetSnapshot(side, 0, [][]module.Segment{segs})
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
	}, vaxis.Style{})
	SetSlotCounts(0, 0, 1)
	SetSpacerSlots(nil, nil, []bool{false})
	SetSlotPriorities(nil, nil, []int{0})
	SetSnapshot(2, 0, w.Levels())

	// 27 columns into 21: the icon and the " * " are untouchable, and the
	// two elastic pieces level off at 8 each.
	if got, want := runsText(fit()[2]), "> TITLETI… * ARTISTA…"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
