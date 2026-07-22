// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/pawbar/pkg/module"
)

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
// by truncation priority.
func buildBlocks() []block {
	cells := map[string][]cell{
		"left":   flatten(0),
		"middle": flatten(1),
		"right":  flatten(2),
	}
	blocks := make([]block, 0, 3)
	for _, name := range truncOrder {
		blocks = append(blocks, block{cells: cells[name], side: anchorOf(name)})
	}
	return blocks
}

func flatten(side int) []cell {
	var out []cell
	for idx, segs := range snapshots[side] {
		for _, seg := range segs {
			hit := Hit{Side: side, Index: idx, Region: seg.Region, Shape: seg.Shape}
			out = append(out, textToCells(seg.Text, seg.Style, hit, true)...)
		}
	}
	return out
}

// textToCells splits text into grapheme cells carrying hit metadata.
func textToCells(s string, style vaxis.Style, hit Hit, hasMod bool) []cell {
	chars := vaxis.Characters(s)
	out := make([]cell, 0, len(chars))
	for _, ch := range chars {
		out = append(out, cell{
			c:      vaxis.Cell{Character: ch, Style: style},
			hit:    hit,
			hasMod: hasMod,
		})
	}
	return out
}

// SegmentsWidth measures rendered segments in bar columns.
func SegmentsWidth(segs []module.Segment) int {
	w := 0
	for _, seg := range segs {
		for _, ch := range vaxis.Characters(seg.Text) {
			w += ch.Width
		}
	}
	return w
}

// writeCell writes one cell (padding wide graphemes) and mirrors it into
// the hit table. Returns x + grapheme width.
func writeCell(win vaxis.Window, x int, c cell) int {
	if x+c.c.Width > width {
		return x + c.c.Width
	}
	win.SetCell(x, 0, c.c)
	state[x] = c

	for w := 1; w < c.c.Width; w++ {
		empty := vaxis.Cell{Style: c.c.Style}
		win.SetCell(x+w, 0, empty)
		state[x+w] = cell{c: empty, hit: c.hit, hasMod: c.hasMod}
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
func trimStart(cells []cell, w int, ellipsis bool) []cell {
	if w <= 0 {
		return nil
	}
	if totalWidth(cells) <= w {
		return cells
	}
	if ellipsis {
		if ellipsisWidth >= w {
			return nil
		}
		w -= ellipsisWidth
	}
	acc := 0
	end := 0
	for ; end < len(cells) && acc < w; end++ {
		acc += cells[end].c.Width
	}
	trim := cells[:end]
	if ellipsis {
		trim = append(clone(trim), ellipsisCells...)
	}
	return trim
}

// trimEnd keeps the trailing cells that fit in w, optionally prepending an
// ellipsis.
func trimEnd(cells []cell, w int, ellipsis bool) []cell {
	if w <= 0 {
		return nil
	}
	if totalWidth(cells) <= w {
		return cells
	}
	if ellipsis {
		if ellipsisWidth >= w {
			return nil
		}
		w -= ellipsisWidth
	}
	acc := 0
	start := len(cells)
	for start > 0 && acc < w {
		start--
		acc += cells[start].c.Width
	}
	trim := cells[start:]
	if ellipsis {
		trim = append(clone(ellipsisCells), trim...)
	}
	return trim
}

// trimMiddle keeps the middle, trimming both ends evenly.
func trimMiddle(cells []cell, w int, ellipsis bool) []cell {
	if w <= 0 || totalWidth(cells) <= w {
		return cells
	}
	if ellipsis {
		ellW := ellipsisWidth * 2
		if ellW >= w {
			return nil
		}
		w -= ellW
	}

	lo, hi := 0, len(cells)-1
	cur := totalWidth(cells)
	for cur > w && lo < hi {
		cur -= cells[lo].c.Width
		lo++
		if cur > w && lo < hi {
			cur -= cells[hi].c.Width
			hi--
		}
	}
	trimmed := cells[lo : hi+1]
	if ellipsis {
		trimmed = append(append(clone(ellipsisCells), trimmed...), ellipsisCells...)
	}
	return trimmed
}

func clone(src []cell) []cell {
	out := make([]cell, len(src))
	copy(out, src)
	return out
}
