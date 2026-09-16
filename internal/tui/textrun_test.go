// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// The shaper comes up in the background and may never come up at all (no
// kitty, no fonts). Layout has to keep working meanwhile, falling back to
// what the terminal makes of the text.
func TestComplexTextFallsBackToPlainCells(t *testing.T) {
	Init(200, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
	}, vaxis.Style{})

	const text = "ab हिन्दी cd"
	cells := textToCells(text, vaxis.Style{}, Hit{}, true, false)
	if len(cells) == 0 {
		t.Fatal("no cells for complex text")
	}
	for i, c := range cells {
		if c.txt != nil {
			t.Fatalf("cell %d carries a run with no shaper up", i)
		}
	}
	if got, want := totalWidth(cells), SegmentsWidth([]module.Segment{{Text: text}}); got != want {
		t.Errorf("width %d from cells, %d from SegmentsWidth", got, want)
	}
}

// Folding the ellipsis into a run is only for run columns; ordinary text
// still gets the ellipsis the terminal draws.
func TestEllipsisStaysPlainWithoutARun(t *testing.T) {
	Init(200, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		Ellipsis:         "…",
	}, vaxis.Style{})

	cells := textToCells("abcdef", vaxis.Style{}, Hit{}, true, false)
	got := withEllipsisAfter(cells)
	if len(got) != len(cells)+len(ellipsisCells) {
		t.Fatalf("got %d cells, want %d", len(got), len(cells)+len(ellipsisCells))
	}
	last := got[len(got)-1]
	if last.txt != nil {
		t.Error("ellipsis was folded into a run that does not exist")
	}
	if last.c.Grapheme != "…" {
		t.Errorf("last cell is %q, want the ellipsis", last.c.Grapheme)
	}
	// The source must not have been written through.
	if len(cells) > 0 && cells[len(cells)-1].c.Grapheme != "f" {
		t.Error("withEllipsisAfter wrote into its argument")
	}
}
