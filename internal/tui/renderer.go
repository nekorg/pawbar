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
	"image"
	"image/color"
	"sync"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/textrun"
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
	// txt is set on every column of a rasterised complex-script run.
	txt *textCell
}

// iconInset leaves a pixel of breathing room around a drawn tray icon.
const iconInset = 1

// cachedRuns is a slot's laid-out runs at one detail level. ok separates
// "not built yet" from "built, and it draws nothing".
type cachedRuns struct {
	runs []run
	ok   bool
}

// imgKey identifies a cached graphic. Cell size is part of it: a dpi or font
// change re-scales the icon rather than reusing a bitmap sized for the old
// cell. Comparable, so indexing costs no formatting.
type imgKey struct {
	key  string
	w, h int
}

// cellPx is the terminal's cell size in pixels. Asking vaxis for it takes its
// lock, so Render asks once and hands it down.
type cellPx struct{ w, h int }

// blankChar is a space. vaxis.Characters would spin up a grapheme iterator
// to tell us the same thing, once per column per frame.
var blankChar = vaxis.Character{Grapheme: " ", Width: 1}

// Layout is one bar surface: its geometry, its slots' snapshots, the cells
// they last laid out to, and the terminal resources that drew them.
//
// None of this can be shared between surfaces. The obvious half is geometry
// and snapshots. The less obvious half is icons: a *vaxis.KittyImage belongs
// to the vaxis that made it, and pruneIcons destroys every graphic the last
// frame did not draw, so two surfaces sharing the cache would place each
// other's image ids and free each other's graphics.
type Layout struct {
	// term is the terminal we lay out for. Grapheme widths have to be
	// measured the way it will render them; uucode's numbers are only right
	// if the terminal happens to segment the same way. nil in tests, which
	// fall back to uucode.
	term *vaxis.Vaxis
	// shaper rasterises the scripts a cell grid cannot hold. nil in tests,
	// and in any bar that never meets one.
	shaper *textrun.Shaper

	width int
	// [side][slot][level] -> segments, levels widest first.
	snapshots  [3][][][]module.Segment
	spacers    [3][]bool // [side][slot] -> spacer module?
	priorities [3][]int  // [side][slot] -> degrade order, lowest first
	// levels[side][slot] is the detail level the last fit chose.
	levels [3][]int
	// slotCache[side][slot][level] is that slot's laid-out runs. Laying a
	// segment out means segmenting graphemes and asking the terminal for
	// every width; between two frames a clock changes one digit and the
	// other nine modules do not move at all.
	slotCache     [3][][]cachedRuns
	state         []cell
	truncOrder    []string
	useEllipsis   bool
	ellipsisCells []cell
	ellipsisWidth int
	gapCells      []cell
	shrinkMin     int

	// icons holds encoded Kitty graphics keyed by content and cell size.
	// Encoding is async and Draw must place the same image every frame, so
	// images are created once and reused; seen tracks which survived the
	// current frame so vanished ones can be freed.
	// occ marks the columns a side has claimed. The fitting pass reads it
	// to place the sides after, and paint reads it to skip the columns
	// nothing drew into: the window was cleared, so blanks are already
	// there and writing them again is the most expensive nothing there is.
	occ []bool

	icons map[imgKey]*vaxis.KittyImage
	seen  map[imgKey]bool

	// The rgb the terminal reports for indexed and default colours, which a
	// rasterised run needs and a cell does not.
	colorMu      sync.Mutex
	colorCache   map[vaxis.Color]color.NRGBA
	colorPending map[vaxis.Color]bool
}

// New builds a layout for one surface. vx measures grapheme widths and owns
// every graphic this layout draws; sh rasterises complex scripts for it and
// may be nil.
func New(vx *vaxis.Vaxis, sh *textrun.Shaper, w int, settings config.BarSettings, gapStyle vaxis.Style) *Layout {
	lay := &Layout{
		term:         vx,
		shaper:       sh,
		icons:        map[imgKey]*vaxis.KittyImage{},
		seen:         map[imgKey]bool{},
		colorCache:   map[vaxis.Color]color.NRGBA{},
		colorPending: map[vaxis.Color]bool{},
	}
	lay.Configure(w, settings, gapStyle)
	return lay
}

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

// Configure applies bar settings and geometry. Called again on reload.
func (lay *Layout) Configure(w int, settings config.BarSettings, gapStyle vaxis.Style) {
	lay.width = w
	lay.truncOrder = settings.TruncatePriority
	lay.useEllipsis = settings.EnableEllipsis == nil || *settings.EnableEllipsis
	lay.ellipsisCells = lay.textToCells(settings.Ellipsis, vaxis.Style{}, Hit{}, false, false)
	lay.ellipsisWidth = totalWidth(lay.ellipsisCells)
	// Gap cells claim no module of their own: hasMod is false so they
	// never absorb a click, and isSpacer lets HitAt donate them to the
	// neighbour the pointer is leaning toward.
	lay.gapCells = lay.textToCells(settings.Gap, gapStyle, Hit{}, false, true)
	lay.shrinkMin = settings.ShrinkMin
	// kitty can report mouse events at the very edge, one past width.
	lay.state = make([]cell, lay.width+1)
	lay.occ = make([]bool, lay.width)
	lay.Invalidate()
}

// SetSlotCounts sizes the snapshot store: one slot per module instance.
func (lay *Layout) SetSlotCounts(l, m, r int) {
	for side, n := range [3]int{l, m, r} {
		lay.snapshots[side] = make([][][]module.Segment, n)
		lay.levels[side] = make([]int, n)
		lay.slotCache[side] = make([][]cachedRuns, n)
	}
}

// SetSlotPriorities records each slot's degrade order: when the bar runs
// out of room, the lowest priority steps down its format ladder first.
// Indexing matches SetSlotCounts.
func (lay *Layout) SetSlotPriorities(l, m, r []int) {
	lay.priorities[0] = l
	lay.priorities[1] = m
	lay.priorities[2] = r
}

// SetSpacerSlots records which slots are spacer modules, so their edge
// cells can donate click area to adjacent modules. Indexing matches
// SetSlotCounts.
func (lay *Layout) SetSpacerSlots(l, m, r []bool) {
	lay.spacers[0] = l
	lay.spacers[1] = m
	lay.spacers[2] = r
	// isSpacer is baked into every cached cell.
	lay.Invalidate()
}

// SetSnapshot stores a slot's latest render output: one segment run per
// detail level, widest first.
func (lay *Layout) SetSnapshot(side, idx int, slotLevels [][]module.Segment) {
	if side < 0 || side > 2 || idx < 0 || idx >= len(lay.snapshots[side]) {
		return
	}
	lay.snapshots[side][idx] = slotLevels
	lay.slotCache[side][idx] = nil
}

// slotLevel is the detail level a slot actually draws at: what fit chose,
// clamped to the ladder it has.
func (lay *Layout) slotLevel(side, idx int) int {
	if n := len(lay.snapshots[side][idx]); n > 0 {
		return min(lay.levels[side][idx], n-1)
	}
	return 0
}

// slotLevels is a slot's per-level run cache, grown to its ladder on first
// use. A slot whose snapshot changed has a nil one and starts over.
func (lay *Layout) slotLevels(side, idx int) []cachedRuns {
	n := max(len(lay.snapshots[side][idx]), 1)
	if len(lay.slotCache[side][idx]) != n {
		lay.slotCache[side][idx] = make([]cachedRuns, n)
	}
	return lay.slotCache[side][idx]
}

// Invalidate drops every slot's laid-out cells. Anything that changes how a
// segment rasterises rather than what it says has to call this: the cache is
// keyed on the snapshot, and none of that is in the snapshot.
func (lay *Layout) Invalidate() {
	for side := range lay.slotCache {
		clear(lay.slotCache[side])
	}
}

// slotSegments returns the segments a slot draws at its currently chosen
// detail level.
func (lay *Layout) slotSegments(side, idx int) []module.Segment {
	slot := lay.snapshots[side][idx]
	if len(slot) == 0 {
		return nil
	}
	return slot[min(lay.levels[side][idx], len(slot)-1)]
}

// Resize adjusts to a new bar width. The bar is one row, so its height
// never took part in the layout.
func (lay *Layout) Resize(w int) {
	lay.width = w
	lay.state = make([]cell, lay.width+1)
	lay.occ = make([]bool, lay.width)
	lay.Invalidate()
}

// HitAt maps a bar column to the slot beneath it. leftHalf tells which half
// of the cell the pointer is on: a spacer cell donates its module-facing
// half to an adjacent non-spacer module, widening that module's hitbox.
func (lay *Layout) HitAt(col int, leftHalf bool) (Hit, bool) {
	if col < 0 || col >= len(lay.state) {
		return Hit{}, false
	}
	c := lay.state[col]
	if c.isSpacer {
		nbr := col + 1
		if leftHalf {
			nbr = col - 1
		}
		if nbr >= 0 && nbr < len(lay.state) {
			n := lay.state[nbr]
			if n.hasMod && !n.isSpacer {
				return n.hit, true
			}
		}
	}
	return c.hit, c.hasMod
}

// Render lays all snapshots out and writes them to the window.
func (lay *Layout) Render(win vaxis.Window) {
	lay.layout()
	lay.paint(win)
}

// layout places every side into state, the bar row as it will be drawn: one
// entry per column, carrying the cell and the hit metadata behind it. It
// touches no window, so what the bar decided to show can be inspected, and
// tested, without a terminal to show it on.
func (lay *Layout) layout() {
	for i := range lay.state {
		lay.state[i] = cell{c: vaxis.Cell{Character: blankChar}}
	}

	blocks := lay.buildBlocks()
	clear(lay.occ)
	occ := lay.occ

	mark := func(x, w int) {
		for i := 0; i < w && x+i < lay.width; i++ {
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
			for free < lay.width && !occ[free] {
				free++
			}
			visible := block.cells
			if fullW > free {
				visible = lay.trimStart(block.cells, free, lay.useEllipsis)
			}
			lay.place(visible, 0, mark)

		case middle:
			start := (lay.width - fullW) / 2
			if start < 0 {
				start = 0
			}
			end := start + fullW

			firstOcc, lastOcc := -1, -1
			for i := start; i < end && i < lay.width; i++ {
				if occ[i] {
					if firstOcc == -1 {
						firstOcc = i
					}
					lastOcc = i
				}
			}
			if firstOcc == -1 {
				lay.place(block.cells, start, mark)
				break
			}

			ellW := 0
			if lay.useEllipsis {
				ellW = lay.ellipsisWidth
			}
			switch {
			case firstOcc == start && lastOcc == end-1:
				// Nothing of its own span survives. It used to go hunting for
				// free columns anywhere in the bar, which put the middle block
				// somewhere that is not the middle and spent room the fitting
				// pass had already promised the side after it. A middle block
				// stays in the middle or it does not draw.

			case firstOcc == start:
				space := end - lastOcc - 1 - ellW
				if space <= 0 {
					break
				}
				visible := lay.trimEnd(block.cells, space, false)
				if lay.useEllipsis {
					visible = lay.withEllipsisBefore(visible)
				}
				lay.place(visible, end-totalWidth(visible), mark)

			case lastOcc == end-1 || firstOcc > start:
				space := firstOcc - start - ellW
				if space <= 0 {
					break
				}
				visible := lay.trimStart(block.cells, space, false)
				if lay.useEllipsis {
					visible = lay.withEllipsisAfter(visible)
				}
				lay.place(visible, start, mark)
			}

		case right:
			free := 0
			for i := lay.width - 1; i >= 0 && !occ[i]; i-- {
				free++
			}
			visible := block.cells
			if fullW > free {
				visible = lay.trimEnd(block.cells, free, lay.useEllipsis)
			}
			if len(visible) == 0 {
				break
			}
			lay.place(visible, lay.width-totalWidth(visible), mark)
		}
	}
}

// place writes a run of cells into the bar row from column x, reporting the
// columns it filled through mark so the sides placed after it know what is
// taken. Drawing happens later, off the row it leaves behind.
func (lay *Layout) place(cells []cell, x int, mark func(int, int)) {
	for _, c := range cells {
		next := lay.placeCell(x, c)
		mark(x, next-x)
		x = next
	}
}

// paint draws the laid-out row: the cells, then the graphics that sit over
// them. Nothing here decides anything; every position was settled by layout.
func (lay *Layout) paint(win vaxis.Window) {
	win.Clear()
	clear(lay.seen)

	var cp cellPx
	if size := win.Vx.Size(); size.Cols > 0 && size.Rows > 0 {
		cp = cellPx{w: size.XPixel / size.Cols, h: size.YPixel / size.Rows}
	}

	n := min(lay.width, len(lay.state))
	runs := false
	for col := range n {
		if !lay.occ[col] {
			continue
		}
		c := lay.state[col]
		win.SetCell(col, 0, c.c)
		// An icon covers its reserved span, and only when the whole span
		// survived: a partially trimmed icon is dropped, not clipped.
		if c.img != nil && col+c.img.span <= lay.width {
			lay.drawIcon(win, col, c.img, cp)
		}
		runs = runs || c.txt != nil
	}

	// A rasterised run is one image over all of its columns that are still
	// here, so gather each run's columns before drawing any of them. Most
	// bars never hold one, so this only sweeps when the pass above saw one.
	for col := 0; runs && col < n; {
		c := lay.state[col]
		if c.txt == nil {
			col++
			continue
		}
		end := col + 1
		for end < n && lay.state[end].txt != nil && lay.state[end].txt.run == c.txt.run {
			end++
		}
		lay.drawTextRun(win, col, lay.state[col:end])
		col = end
	}

	lay.pruneIcons()
}

// drawIcon places an icon segment's Kitty graphic over span columns from
// col, scaled to one row and centered pixel-precisely (mirrors the menu
// gutter-icon renderer). Images are cached by key + cell size across
// frames.
func (lay *Layout) drawIcon(win vaxis.Window, col int, ic *imgCell, cp cellPx) {
	cellW, cellH := cp.w, cp.h

	cacheKey := imgKey{key: ic.key, w: cellW, h: cellH}
	lay.seen[cacheKey] = true

	kimg, cached := lay.icons[cacheKey]
	if !cached {
		kimg = win.Vx.NewKittyGraphic(ic.img)
		lay.icons[cacheKey] = kimg
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
func (lay *Layout) pruneIcons() {
	for key, kimg := range lay.icons {
		if !lay.seen[key] {
			kimg.Destroy()
			delete(lay.icons, key)
		}
	}
}
