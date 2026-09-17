// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"image"
	"image/color"
	"sync"

	"github.com/nekorg/pawbar/internal/textrun"
	"go.rockorager.dev/vaxis"
)

// textCell marks a column carrying a rasterised complex-script run: idx is
// the column's place in the run, or -1 for an ellipsis column folded into it.
type textCell struct {
	run *textrun.Run
	idx int
}

// runCells reserves the columns a complex run needs. They are ordinary blank
// cells, so their background, their underline and their hit metadata all
// still come from the terminal; only the ink is ours.
func runCells(r *textrun.Run, style vaxis.Style, hit Hit, hasMod, spacer bool) []cell {
	blank := vaxis.Cell{Character: blankChar, Style: style}
	out := make([]cell, 0, r.Cells())
	for i := range r.Cells() {
		out = append(out, cell{
			c: blank, hit: hit, hasMod: hasMod, isSpacer: spacer,
			txt: &textCell{run: r, idx: i},
		})
	}
	return out
}

// withEllipsisAfter appends the ellipsis, folding it into a complex run when
// the cut landed in one. The run then draws its own ellipsis inside the
// image, flush against the ink; a terminal ellipsis would sit in the next
// whole cell, up to a cell away from the text it belongs to.
func withEllipsisAfter(cells []cell) []cell {
	out := clone(cells)
	if n := len(out); n > 0 && out[n-1].txt != nil {
		return append(out, ellipsisFor(out[n-1])...)
	}
	return append(out, ellipsisCells...)
}

// withEllipsisBefore is withEllipsisAfter at the other end.
func withEllipsisBefore(cells []cell) []cell {
	if len(cells) > 0 && cells[0].txt != nil {
		return append(ellipsisFor(cells[0]), cells...)
	}
	return append(clone(ellipsisCells), cells...)
}

// ellipsisFor is the ellipsis handed to from's run as bare columns: the
// terminal writes nothing in them, and the rasteriser spends them on the
// ellipsis it draws inside the image. Trimming an already trimmed run folds
// again, which only ever moves columns between the two, never adds a second
// ellipsis.
func ellipsisFor(from cell) []cell {
	out := make([]cell, 0, len(ellipsisCells))
	for _, e := range ellipsisCells {
		c := from
		c.c.Character = vaxis.Character{Grapheme: " ", Width: e.c.Width}
		c.txt = &textCell{run: from.txt.run, idx: -1}
		out = append(out, c)
	}
	return out
}

// drawTextRun places one run's columns as a kitty graphic. The image is
// exactly the group's columns wide and one row tall, rasterised at the
// terminal's own em and baseline, so it lands on integer pixels with nothing
// to resample and nothing to centre.
func drawTextRun(win vaxis.Window, col int, group []cell) {
	cellW, cellH := textrun.CellSize()
	if cellW <= 0 || cellH <= 0 || col < 0 {
		return
	}
	span := min(len(group), width-col)
	if span <= 0 {
		return
	}

	run := group[0].txt.run
	lo, hi := -1, -1
	for _, c := range group[:span] {
		if c.txt.idx < 0 {
			continue
		}
		if lo < 0 {
			lo = c.txt.idx
		}
		hi = c.txt.idx
	}
	// A second trim can leave the ellipsis columns standing with none of the
	// text they were cutting short; draw the ellipsis alone rather than a gap.
	fg := fgOf(group[0].c.Style)
	var img *image.NRGBA
	var key string
	if lo < 0 {
		img, key = run.Ellipsis(span, fg)
	} else {
		img, key = run.Image(span, lo > 0, hi < run.Cells()-1, fg)
	}
	if img == nil {
		return
	}

	cacheKey := imgKey{key: key, w: cellW, h: cellH}
	iconSeen[cacheKey] = true
	kimg, cached := iconCache[cacheKey]
	if !cached {
		kimg = win.Vx.NewKittyPixels(img)
		iconCache[cacheKey] = kimg
	}
	kimg.Draw(win.New(col, 0, span, 1))
}

// fgOf is the colour a run's ink takes. A rasterised run needs real rgb where
// a cell only needs a terminal colour, so indexed and default colours have to
// be asked about. That is a round trip, and doing it from the render loop can
// deadlock vaxis, so a miss draws in a placeholder and repaints once the
// answer is in.
func fgOf(st vaxis.Style) color.NRGBA {
	c := st.Foreground
	if st.Attribute&vaxis.AttrReverse != 0 {
		c = st.Background
	}
	if p := c.Params(); len(p) == 3 {
		return color.NRGBA{R: p[0], G: p[1], B: p[2], A: 0xff}
	}

	colorMu.Lock()
	defer colorMu.Unlock()
	if v, ok := colorCache[c]; ok {
		return v
	}
	if !colorPending[c] {
		colorPending[c] = true
		go resolveColor(c)
	}
	return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
}

var (
	colorMu      sync.Mutex
	colorCache   = map[vaxis.Color]color.NRGBA{}
	colorPending = map[vaxis.Color]bool{}
)

func resolveColor(c vaxis.Color) {
	vx := term
	if vx == nil {
		return
	}
	var got vaxis.Color
	if len(c.Params()) == 0 {
		got = vx.QueryForeground()
	} else {
		got = vx.QueryColor(c)
	}
	p := got.Params()
	if len(p) != 3 {
		return
	}
	colorMu.Lock()
	colorCache[c] = color.NRGBA{R: p[0], G: p[1], B: p[2], A: 0xff}
	colorMu.Unlock()
	vx.PostEvent(vaxis.Redraw{})
}

// ForgetColors drops the resolved palette. Call on a theme change: the
// terminal's answers to those queries have just changed.
func ForgetColors() {
	colorMu.Lock()
	defer colorMu.Unlock()
	clear(colorCache)
	clear(colorPending)
}
