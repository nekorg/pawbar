// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"github.com/nekorg/pawbar/internal/textrun"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// splitOrWhole is [textrun.Split] for callers that only want to walk the
// parts, whether or not any of them need rasterising.
func splitOrWhole(s string) []textrun.Part {
	if parts := textrun.Split(s); parts != nil {
		return parts
	}
	return []textrun.Part{{Text: s}}
}

func anchorOf(s string) anchor {
	switch s {
	case "left":
		return left
	case "middle":
		return middle
	default:
		return right
	}
}

// buildBlocks flattens the snapshots into one cell run per side, ordered
// by truncation priority, with elastic segments already shrunk to fit.
func (lay *Layout) buildBlocks() []block {
	runs := lay.fit()

	sides := map[string]int{"left": 0, "middle": 1, "right": 2}
	blocks := make([]block, 0, 3)
	for _, name := range lay.truncOrder {
		var cells []cell
		for _, r := range runs[sides[name]] {
			cells = append(cells, r.cells...)
		}
		blocks = append(blocks, block{cells: cells, side: anchorOf(name)})
	}
	return blocks
}

// flatten lays a side's slots out as runs — one per segment, so the fitting
// pass can shrink the elastic ones individually — dropping spacers that have
// gone stranded and inserting the configured gap between neighbours.
func (lay *Layout) flatten(side int) []run {
	visible := lay.visibleSlots(side)

	var out []run
	prevSpacer, first := false, true
	for _, idx := range visible {
		spacer := lay.isSpacer(side, idx)
		if !first && len(lay.gapCells) > 0 && !prevSpacer && !spacer {
			out = append(out, run{cells: clone(lay.gapCells)})
		}
		first, prevSpacer = false, spacer
		out = append(out, lay.slotRuns(side, idx, spacer)...)
	}
	return out
}

// slotRuns is one slot's segments laid out at its current detail level.
//
// The runs are cached and handed out shared: the fitting pass only ever
// reassigns run.cells to a sub-slice or to a freshly cloned one, and
// buildBlocks copies every cell out before Render trims anything, so nothing
// downstream writes through into the cache.
func (lay *Layout) slotRuns(side, idx int, spacer bool) []run {
	lvl := lay.slotLevel(side, idx)
	levelCache := lay.slotLevels(side, idx)
	if lvl < len(levelCache) && levelCache[lvl].ok {
		return levelCache[lvl].runs
	}

	var out []run
	for _, seg := range lay.slotSegments(side, idx) {
		hit := Hit{Side: side, Index: idx, Region: seg.Region, Shape: seg.Shape}
		switch c := seg.Content.(type) {
		case module.Image:
			if c.Img == nil || c.Cells <= 0 {
				continue
			}
			out = append(out, run{cells: imageCells(c, seg.Style, hit, spacer)})
		case module.Text:
			out = append(out, run{
				cells:  lay.textToCells(c.S, seg.Style, hit, true, spacer),
				shrink: c.Shrink,
			})
		}
	}
	if lvl < len(levelCache) {
		levelCache[lvl] = cachedRuns{runs: out, ok: true}
	}
	return out
}

func (lay *Layout) isSpacer(side, idx int) bool {
	return idx < len(lay.spacers[side]) && lay.spacers[side][idx]
}

// visibleSlots returns the slots of a side that take part in the layout, in
// order: everything that draws, plus spacers, which count even when empty.
//
// A slot with no segments contributes nothing, which leaves separators
// stranded: `[cpu, gap: " │ ", mpris]` with no player would draw a trailing
// "cpu │ ". So a spacer is dropped when the modules on a side it faces
// exist but are all empty. Facing *nothing* is different from facing
// something empty: a trailing divider at the edge of a side faces the rest
// of the bar rather than a neighbour, and is kept.
func (lay *Layout) visibleSlots(side int) []int {
	n := len(lay.snapshots[side])
	empty := make([]bool, n)
	for i := range n {
		empty[i] = len(lay.slotSegments(side, i)) == 0
	}

	// anyL[i] reports whether any non-spacer slot below i has output,
	// and existsL[i] whether any non-spacer slot is there at all.
	anyL, existsL := make([]bool, n+1), make([]bool, n+1)
	for i := range n {
		anyL[i+1], existsL[i+1] = anyL[i], existsL[i]
		if lay.isSpacer(side, i) {
			continue
		}
		existsL[i+1] = true
		anyL[i+1] = anyL[i] || !empty[i]
	}
	anyR, existsR := make([]bool, n+1), make([]bool, n+1)
	for i := n - 1; i >= 0; i-- {
		anyR[i], existsR[i] = anyR[i+1], existsR[i+1]
		if lay.isSpacer(side, i) {
			continue
		}
		existsR[i] = true
		anyR[i] = anyR[i+1] || !empty[i]
	}

	out := make([]int, 0, n)
	for i := range n {
		if lay.isSpacer(side, i) {
			if (existsL[i] && !anyL[i]) || (existsR[i+1] && !anyR[i+1]) {
				continue
			}
			// An empty spacer is kept: `- gap: ""` draws nothing, and its
			// whole purpose is to sit here suppressing the automatic gap.
			out = append(out, i)
			continue
		}
		if empty[i] {
			continue
		}
		out = append(out, i)
	}
	return out
}

// imageCells reserves img.Cells blank columns for an icon segment, tagging
// the first with the image so the renderer draws it spanning those columns.
// The reserved cells share one Hit so clicks route to the segment's region.
func imageCells(img module.Image, style vaxis.Style, hit Hit, spacer bool) []cell {
	out := make([]cell, 0, img.Cells)
	blank := vaxis.Cell{Character: blankChar, Style: style}
	for i := 0; i < img.Cells; i++ {
		c := cell{c: blank, hit: hit, hasMod: true, isSpacer: spacer}
		if i == 0 {
			c.img = &imgCell{img: img.Img, key: img.Key, span: img.Cells}
		}
		out = append(out, c)
	}
	return out
}

// textToCells splits text into grapheme cells carrying hit metadata. Scripts
// a cell grid cannot hold are split off first and laid out as rasterised
// runs; everything else stays real terminal text.
func (lay *Layout) textToCells(s string, style vaxis.Style, hit Hit, hasMod, spacer bool) []cell {
	parts := textrun.Split(s)
	if parts == nil {
		return lay.plainCells(s, style, hit, hasMod, spacer)
	}
	var out []cell
	bold := style.Attribute&vaxis.AttrBold != 0
	italic := style.Attribute&vaxis.AttrItalic != 0
	for _, p := range parts {
		// The shaper comes up in the background, so until it does this
		// falls through to the terminal and repaints when it is ready.
		if p.Complex {
			if r := textrun.Shape(p.Text, bold, italic); r != nil {
				out = append(out, runCells(r, style, hit, hasMod, spacer)...)
				continue
			}
		}
		out = append(out, lay.plainCells(p.Text, style, hit, hasMod, spacer)...)
	}
	return out
}

func (lay *Layout) plainCells(s string, style vaxis.Style, hit Hit, hasMod, spacer bool) []cell {
	chars := vaxis.Characters(s)
	out := make([]cell, 0, len(chars))
	for _, ch := range chars {
		if lay.term != nil {
			ch.Width = lay.term.CharacterWidth(ch.Grapheme)
		}
		out = append(out, cell{
			c:        vaxis.Cell{Character: ch, Style: style},
			hit:      hit,
			hasMod:   hasMod,
			isSpacer: spacer,
		})
	}
	return out
}

// SegmentsWidth measures rendered segments in bar columns.
func (lay *Layout) SegmentsWidth(segs []module.Segment) int {
	w := 0
	for _, seg := range segs {
		var text string
		switch c := seg.Content.(type) {
		case module.Image:
			if c.Img != nil && c.Cells > 0 {
				w += c.Cells
			}
			continue
		case module.Text:
			text = c.S
		default:
			continue
		}
		for _, p := range splitOrWhole(text) {
			if p.Complex {
				if r := textrun.Shape(p.Text, false, false); r != nil {
					w += r.Cells()
					continue
				}
			}
			for _, ch := range vaxis.Characters(p.Text) {
				if lay.term != nil {
					ch.Width = lay.term.CharacterWidth(ch.Grapheme)
				}
				w += ch.Width
			}
		}
	}
	return w
}

// placeCell puts one cell in the bar row at x, padding a wide grapheme
// across the columns it covers so the hit table answers for all of them.
// Returns x + grapheme width.
func (lay *Layout) placeCell(x int, c cell) int {
	if c.c.Width == 0 {
		// Layout budgeted no column for this, but vaxis spends one on any
		// cell it holds. Drop it instead of letting it push the row over.
		return x
	}
	if x+c.c.Width > lay.width {
		return x + c.c.Width
	}
	lay.state[x] = c

	for w := 1; w < c.c.Width; w++ {
		empty := vaxis.Cell{Style: c.c.Style}
		lay.state[x+w] = cell{c: empty, hit: c.hit, hasMod: c.hasMod, isSpacer: c.isSpacer}
	}
	return x + c.c.Width
}

func totalWidth(cells []cell) int {
	w := 0
	for _, c := range cells {
		w += c.c.Width
	}
	return w
}

// trimStart keeps the leading cells that fit in w, optionally appending an
// ellipsis.
func (lay *Layout) trimStart(cells []cell, w int, ellipsis bool) []cell {
	if w <= 0 {
		return nil
	}
	if totalWidth(cells) <= w {
		return cells
	}
	if ellipsis {
		if lay.ellipsisWidth >= w {
			return nil
		}
		w -= lay.ellipsisWidth
	}
	acc := 0
	end := 0
	for ; end < len(cells) && acc < w; end++ {
		acc += cells[end].c.Width
	}
	trim := cells[:end]
	if ellipsis {
		trim = lay.withEllipsisAfter(trim)
	}
	return trim
}

// trimEnd keeps the trailing cells that fit in w, optionally prepending an
// ellipsis.
func (lay *Layout) trimEnd(cells []cell, w int, ellipsis bool) []cell {
	if w <= 0 {
		return nil
	}
	if totalWidth(cells) <= w {
		return cells
	}
	if ellipsis {
		if lay.ellipsisWidth >= w {
			return nil
		}
		w -= lay.ellipsisWidth
	}
	acc := 0
	start := len(cells)
	for start > 0 && acc < w {
		start--
		acc += cells[start].c.Width
	}
	trim := cells[start:]
	if ellipsis {
		trim = lay.withEllipsisBefore(trim)
	}
	return trim
}

func clone(src []cell) []cell {
	out := make([]cell, len(src))
	copy(out, src)
	return out
}
