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
	"fmt"
	"image"

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

// imgCell marks the first reserved column of an icon segment: the runtime
// draws img as a Kitty graphic spanning span columns from this cell.
type imgCell struct {
	img  image.Image
	key  string
	span int
}

// cell is one laid-out grapheme plus its hit metadata. img is set only on
// the head column of an icon segment.
type cell struct {
	c        vaxis.Cell
	hit      Hit
	hasMod   bool
	isSpacer bool
	img      *imgCell
}

// iconInset leaves a pixel of breathing room around a drawn tray icon.
const iconInset = 1

// iconCache holds encoded Kitty graphics keyed by ImageKey. Encoding is
// async and Draw must place the same image every frame, so images are
// created once and reused; iconSeen tracks which survived the current frame
// so vanished ones can be freed.
var (
	iconCache = map[string]*vaxis.KittyImage{}
	iconSeen  = map[string]bool{}
)

var (
	width, height int
	snapshots     [3][][]module.Segment // [side][slot] -> segments
	spacers       [3][]bool             // [side][slot] -> spacer module?
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
	ellipsisCells = textToCells(settings.Ellipsis, vaxis.Style{}, Hit{}, false, false)
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

// SetSpacerSlots records which slots are spacer modules, so their edge
// cells can donate click area to adjacent modules. Indexing matches
// SetSlotCounts.
func SetSpacerSlots(l, m, r []bool) {
	spacers[0] = l
	spacers[1] = m
	spacers[2] = r
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

// HitAt maps a bar column to the slot beneath it. leftHalf tells which half
// of the cell the pointer is on: a spacer cell donates its module-facing
// half to an adjacent non-spacer module, widening that module's hitbox.
func HitAt(col int, leftHalf bool) (Hit, bool) {
	if col < 0 || col >= len(state) {
		return Hit{}, false
	}
	c := state[col]
	if c.isSpacer {
		nbr := col + 1
		if leftHalf {
			nbr = col - 1
		}
		if nbr >= 0 && nbr < len(state) {
			n := state[nbr]
			if n.hasMod && !n.isSpacer {
				return n.hit, true
			}
		}
	}
	return c.hit, c.hasMod
}

// Render lays all snapshots out and writes them to the window.
func Render(win vaxis.Window) {
	for i := range state {
		state[i] = cell{c: vaxis.Cell{Character: vaxis.Characters(" ")[0]}}
	}
	win.Clear()
	clear(iconSeen)

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

	pruneIcons()
}

func drawCells(win vaxis.Window, cells []cell, x int, mark func(int, int)) {
	if len(cells) == 0 {
		return
	}
	for _, r := range cells {
		start := x
		next := writeCell(win, x, r)
		mark(x, next-x)
		// Draw the icon over its reserved span only when it fits whole;
		// a partially trimmed icon is dropped rather than clipped.
		if r.img != nil && start+r.img.span <= width {
			drawIcon(win, start, r.img)
		}
		x = next
	}
}

// drawIcon places an icon segment's Kitty graphic over span columns from
// col, scaled to one row and centered pixel-precisely (mirrors the menu
// gutter-icon renderer). Images are cached by key + cell size across
// frames.
func drawIcon(win vaxis.Window, col int, ic *imgCell) {
	var cellW, cellH int
	if size := win.Vx.Size(); size.Cols > 0 && size.Rows > 0 {
		cellW = size.XPixel / size.Cols
		cellH = size.YPixel / size.Rows
	}

	// Cell size is part of the key: a DPI/font change re-scales the icon
	// rather than reusing a bitmap sized for the old cell.
	cacheKey := fmt.Sprintf("%s@%dx%d", ic.key, cellW, cellH)
	iconSeen[cacheKey] = true

	kimg, cached := iconCache[cacheKey]
	if !cached {
		kimg = win.Vx.NewKittyGraphic(ic.img)
		iconCache[cacheKey] = kimg
		if cellW > 0 && cellH > 0 {
			// vaxis resamples with a high-quality filter, so this yields a
			// crisp icon fitted to the gutter box.
			kimg.ResizePixels(ic.span*cellW-2*iconInset, cellH-2*iconInset)
		} else {
			kimg.Resize(ic.span, 1)
		}
	}

	if cellW <= 0 || cellH <= 0 {
		kimg.Draw(win.New(col, 0, ic.span, 1))
		return
	}

	// Center within the span: the absolute offset splits into an anchor
	// cell plus an intra-cell rest, since kitty's X placement key must
	// stay below one cell.
	spanW := ic.span * cellW
	pw, ph := kimg.PixelSize()
	ox := max(0, (spanW-pw)/2)
	yOff := max(0, (cellH-ph)/2)
	kimg.SetOffset(ox%cellW, yOff)
	kimg.Draw(win.New(col+ox/cellW, 0, ic.span-ox/cellW, 1))
}

// pruneIcons frees Kitty graphics whose segments were not drawn this frame
// (e.g. a tray item disappeared), so terminal image memory doesn't grow.
func pruneIcons() {
	for key, kimg := range iconCache {
		if !iconSeen[key] {
			kimg.Destroy()
			delete(iconCache, key)
		}
	}
}
