// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package tui lays a bar's module snapshots out into a vaxis window:
// left/middle/right anchoring, truncation by priority, ellipsis, and a
// per-column hit table that maps mouse positions back to slots.
package tui

import (
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// Hit identifies what lives under a bar column.
type Hit struct {
	Side   int // core.Side values: 0 left, 1 middle, 2 right
	Index  int
	Region string
	Shape  vaxis.MouseShape
}

// cell is one laid-out grapheme plus its hit metadata.
type cell struct {
	c      vaxis.Cell
	hit    Hit
	hasMod bool
}

var (
	width, height int
	snapshots     [3][][]module.Segment // [side][slot] -> segments
	state         []cell
	truncOrder    []string
	useEllipsis   bool
	ellipsisCells []cell
	ellipsisWidth int
)

type anchor int

const (
	left anchor = iota
	middle
	right
)

type block struct {
	cells []cell
	side  anchor
}

// Init prepares the layout state. Can be called again (reload).
func Init(w, h int, settings config.BarSettings) {
	width, height = w, h
	truncOrder = settings.TruncatePriority
	useEllipsis = settings.EnableEllipsis == nil || *settings.EnableEllipsis
	ellipsisCells = textToCells(settings.Ellipsis, vaxis.Style{}, Hit{}, false)
	ellipsisWidth = totalWidth(ellipsisCells)
	// kitty can report mouse events at the very edge, one past width.
	state = make([]cell, width+1)
}

// SetSlotCounts sizes the snapshot store: one slot per module instance.
func SetSlotCounts(l, m, r int) {
	snapshots[0] = make([][]module.Segment, l)
	snapshots[1] = make([][]module.Segment, m)
	snapshots[2] = make([][]module.Segment, r)
}

// SetSnapshot stores a slot's latest render output.
func SetSnapshot(side, idx int, segs []module.Segment) {
	if side < 0 || side > 2 || idx < 0 || idx >= len(snapshots[side]) {
		return
	}
	snapshots[side][idx] = segs
}

// Resize adjusts to a new window size.
func Resize(w, h int) {
	width, height = w, h
	state = make([]cell, width+1)
}

// HitAt maps a bar column to the slot beneath it.
func HitAt(col int) (Hit, bool) {
	if col < 0 || col >= len(state) {
		return Hit{}, false
	}
	c := state[col]
	return c.hit, c.hasMod
}

// Render lays all snapshots out and writes them to the window.
func Render(win vaxis.Window) {
	for i := range state {
		state[i] = cell{c: vaxis.Cell{Character: vaxis.Characters(" ")[0]}}
	}
	win.Clear()

	blocks := buildBlocks()
	occ := make([]bool, width)

	mark := func(x, w int) {
		for i := 0; i < w && x+i < width; i++ {
			occ[x+i] = true
		}
	}

	for _, block := range blocks {
		if len(block.cells) == 0 {
			continue
		}

		fullW := totalWidth(block.cells)
		switch block.side {
		case left:
			free := 0
			for free < width && !occ[free] {
				free++
			}
			visible := block.cells
			if fullW > free {
				visible = trimStart(block.cells, free, useEllipsis)
			}
			drawCells(win, visible, 0, mark)

		case middle:
			start := (width - fullW) / 2
			if start < 0 {
				start = 0
			}
			end := start + fullW

			firstOcc, lastOcc := -1, -1
			for i := start; i < end && i < width; i++ {
				if occ[i] {
					if firstOcc == -1 {
						firstOcc = i
					}
					lastOcc = i
				}
			}
			if firstOcc == -1 {
				drawCells(win, block.cells, start, mark)
				break
			}

			ellW := 0
			if useEllipsis {
				ellW = ellipsisWidth
			}
			switch {
			case firstOcc == start && lastOcc == end-1:
				gapStart := 0
				for gapStart < width && occ[gapStart] {
					gapStart++
				}
				gapEnd := width - 1
				for gapEnd >= 0 && occ[gapEnd] {
					gapEnd--
				}
				gapLen := gapEnd - gapStart + 1
				if gapLen <= 0 {
					// No room for this block; later blocks may still fit.
					break
				}
				if gapLen-2*ellW > 0 {
					visible := trimMiddle(block.cells, gapLen, useEllipsis)
					drawCells(win, visible, gapStart+(gapLen-totalWidth(visible))/2, mark)
				}

			case firstOcc == start:
				space := end - lastOcc - 1 - ellW
				if space <= 0 {
					break
				}
				visible := trimEnd(block.cells, space, false)
				if useEllipsis {
					visible = append(clone(ellipsisCells), visible...)
				}
				drawCells(win, visible, end-totalWidth(visible), mark)

			case lastOcc == end-1 || firstOcc > start:
				space := firstOcc - start - ellW
				if space <= 0 {
					break
				}
				visible := trimStart(block.cells, space, false)
				if useEllipsis {
					visible = append(visible, ellipsisCells...)
				}
				drawCells(win, visible, start, mark)
			}

		case right:
			free := 0
			for i := width - 1; i >= 0 && !occ[i]; i-- {
				free++
			}
			visible := block.cells
			if fullW > free {
				visible = trimEnd(block.cells, free, useEllipsis)
			}
			if len(visible) == 0 {
				break
			}
			drawCells(win, visible, width-totalWidth(visible), mark)
		}
	}
}

func drawCells(win vaxis.Window, cells []cell, x int, mark func(int, int)) {
	if len(cells) == 0 {
		return
	}
	for _, r := range cells {
		next := writeCell(win, x, r)
		mark(x, next-x)
		x = next
	}
}
