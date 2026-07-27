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

// layout state is package-global, so these tests reseed it and cannot run
// in parallel.

// setup lays out one side (left) from a slot description: each slot is its
// rendered text ("" for a module that rendered nothing) and whether it is a
// spacer module.
func setup(t *testing.T, gap string, slots []slot) {
	t.Helper()
	settings := config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
		Gap:              gap,
	}
	Init(200, 1, settings, vaxis.Style{})
	SetSlotCounts(len(slots), 0, 0)

	spacerFlags := make([]bool, len(slots))
	for i, s := range slots {
		spacerFlags[i] = s.spacer
	}
	SetSpacerSlots(spacerFlags, nil, nil)

	for i, s := range slots {
		if s.text == "" {
			SetSnapshot(0, i, [][]module.Segment{nil})
			continue
		}
		SetSnapshot(0, i, [][]module.Segment{{{Text: s.text}}})
	}
}

type slot struct {
	text   string
	spacer bool
}

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

func runsCells(runs []run) []cell {
	var out []cell
	for _, r := range runs {
		out = append(out, r.cells...)
	}
	return out
}

func TestFlattenSuppressesStrandedSpacers(t *testing.T) {
	cases := []struct {
		name  string
		slots []slot
		want  string
	}{
		{
			// The live bug: mpris with no player leaves "cpu │ ".
			name:  "spacer before an empty module",
			slots: []slot{{text: "cpu"}, {text: " │ ", spacer: true}, {text: ""}},
			want:  "cpu",
		},
		{
			name:  "spacer after an empty module",
			slots: []slot{{text: ""}, {text: " │ ", spacer: true}, {text: "clock"}},
			want:  "clock",
		},
		{
			// A trailing divider divides this side from the rest of the
			// bar; it faces nothing, not something empty.
			name:  "trailing spacer at the side edge survives",
			slots: []slot{{text: "ws"}, {text: " │ ", spacer: true}},
			want:  "ws │ ",
		},
		{
			name:  "leading spacer at the side edge survives",
			slots: []slot{{text: " │ ", spacer: true}, {text: "ws"}},
			want:  " │ ws",
		},
		{
			name:  "spacer between two live modules survives",
			slots: []slot{{text: "cpu"}, {text: " │ ", spacer: true}, {text: "clock"}},
			want:  "cpu │ clock",
		},
		{
			name: "spacer facing a live module past an empty one survives",
			slots: []slot{
				{text: "cpu"}, {text: " │ ", spacer: true}, {text: ""}, {text: "clock"},
			},
			want: "cpu │ clock",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setup(t, "", c.slots)
			if got := runsText(flatten(0)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestFlattenGap(t *testing.T) {
	cases := []struct {
		name  string
		gap   string
		slots []slot
		want  string
	}{
		{
			name:  "off by default",
			gap:   "",
			slots: []slot{{text: "cpu"}, {text: "ram"}, {text: "clock"}},
			want:  "cpuramclock",
		},
		{
			name:  "between adjacent modules",
			gap:   " ",
			slots: []slot{{text: "cpu"}, {text: "ram"}, {text: "clock"}},
			want:  "cpu ram clock",
		},
		{
			// An explicit gap is the join; doubling it up is not wanted.
			name:  "never adjacent to a spacer",
			gap:   " ",
			slots: []slot{{text: "ws"}, {text: " │ ", spacer: true}, {text: "clock"}},
			want:  "ws │ clock",
		},
		{
			name:  "empty module does not leave a double gap",
			gap:   " ",
			slots: []slot{{text: "cpu"}, {text: ""}, {text: "clock"}},
			want:  "cpu clock",
		},
		{
			name:  "no leading or trailing gap",
			gap:   " ",
			slots: []slot{{text: "cpu"}},
			want:  "cpu",
		},
		{
			// `- gap: ""`: an empty spacer draws nothing but still stands
			// between its neighbours, so the automatic gap stays away.
			name:  "an empty spacer joins its neighbours",
			gap:   " ",
			slots: []slot{{text: "cpu"}, {text: "", spacer: true}, {text: "ram"}},
			want:  "cpuram",
		},
		{
			name:  "an explicit spacer overrides the gap width",
			gap:   " ",
			slots: []slot{{text: "cpu"}, {text: "   ", spacer: true}, {text: "ram"}},
			want:  "cpu   ram",
		},
		{
			// Spacers join what is actually adjacent on screen, so a
			// vanished module in between is simply skipped over — the same
			// rule a divider follows.
			name: "a joiner reaches past an empty module",
			gap:  " ",
			slots: []slot{
				{text: "cpu"}, {text: "", spacer: true}, {text: ""}, {text: "clock"},
			},
			want: "cpuclock",
		},
		{
			// With nothing live left to face, it strands like any spacer.
			name:  "a joiner facing only empty modules is dropped",
			gap:   " ",
			slots: []slot{{text: "cpu"}, {text: "", spacer: true}, {text: ""}},
			want:  "cpu",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setup(t, c.gap, c.slots)
			if got := runsText(flatten(0)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// A gap must not swallow clicks: it carries no module, and HitAt donates it
// to whichever neighbour the pointer leans toward.
func TestGapDonatesHitArea(t *testing.T) {
	setup(t, " ", []slot{{text: "cpu"}, {text: "ram"}})
	copy(state, runsCells(flatten(0)))

	const gapCol = 3 // "cpu" then the gap
	if state[gapCol].hasMod {
		t.Fatalf("gap cell should carry no module")
	}
	hit, ok := HitAt(gapCol, true)
	if !ok || hit.Index != 0 {
		t.Errorf("left half of the gap: got %+v ok=%v, want slot 0", hit, ok)
	}
	hit, ok = HitAt(gapCol, false)
	if !ok || hit.Index != 1 {
		t.Errorf("right half of the gap: got %+v ok=%v, want slot 1", hit, ok)
	}
}
