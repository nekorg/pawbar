// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package textrun draws the scripts a terminal cell grid cannot hold.
//
// Devanagari and its neighbours reorder, stack and join, and their spacing
// marks carry width that every terminal width table scores as zero. The
// terminal therefore allots a fraction of the columns the text needs and
// overlaps the rest, and no amount of width pinning fixes that: the cell
// count is the contract, and the text does not fit it.
//
// So for those scripts, and only those, we shape and rasterise the phrase
// ourselves at the terminal's own em and baseline, and place the result as a
// graphic over blank cells. The cells keep their background, their underline
// and their hit metadata; only the ink comes from us.
package textrun

import (
	"sync"

	"github.com/nekorg/pawbar/internal/logging"
)

// Options configure the shaper. Everything here is known at startup.
type Options struct {
	// KittyCmd and KittyConfig are what the panel's own kitty was started
	// with, so the probe resolves the same fonts it did.
	KittyCmd    string
	KittyConfig string
	// FontSize is the size pinned on the panel, in points.
	FontSize float64
	// Family overrides the font family, empty to take kitty's own.
	Family string
	// BaselineNudge shifts every rasterised run vertically, for the case
	// where the probe is unavailable and the derived baseline is a pixel
	// out.
	BaselineNudge int
	// Notify is called once when the shaper becomes usable, so the caller
	// can repaint the frames that fell back to plain cells.
	Notify func()
}

var (
	mu   sync.RWMutex
	opts Options
	// cellW, cellH is the panel's cell in pixels, as vaxis measured it.
	cellW, cellH int

	// gen counts how many times the shaper has been thrown away, so a build
	// that was already running when that happened knows not to install
	// itself. loadOnce starts the next one, and is replaced with it.
	gen      int
	loadOnce = &sync.Once{}
	loaded   bool
	state    *shaper
)

// Init records how the bar was started. It does no work: the shaper only
// comes up if a complex run actually turns up, and the font index alone
// costs more than every module in the bar put together. Safe to call again
// on a config reload, which rebuilds the shaper if anything it depends on
// moved.
func Init(o Options) {
	mu.Lock()
	defer mu.Unlock()
	changed := o.KittyCmd != opts.KittyCmd || o.KittyConfig != opts.KittyConfig ||
		o.FontSize != opts.FontSize || o.Family != opts.Family ||
		o.BaselineNudge != opts.BaselineNudge
	opts = o
	if changed {
		invalidate()
	}
}

// SetCellSize records the panel's cell in pixels. Everything the shaper
// measures comes off the cell, so a change throws it away and rebuilds it.
func SetCellSize(w, h int) {
	mu.Lock()
	defer mu.Unlock()
	if w == cellW && h == cellH {
		return
	}
	cellW, cellH = w, h
	logging.Log.Debug().Msgf("textrun: cell is now %dx%d", w, h)
	invalidate()
}

// invalidate throws the shaper away so the next complex run builds a fresh
// one. Callers hold mu.
func invalidate() {
	gen++
	loaded = false
	state = nil
	loadOnce = &sync.Once{}
}

// ready reports whether the shaper can be used, starting it the first time it
// is asked. Until it is up, callers fall back to plain terminal cells; the
// Notify hook repaints once that stops being necessary.
func ready() (*shaper, bool) {
	mu.RLock()
	if loaded {
		s := state
		mu.RUnlock()
		return s, s != nil
	}
	o, w, h, g, once := opts, cellW, cellH, gen, loadOnce
	mu.RUnlock()

	if w <= 0 || h <= 0 {
		return nil, false
	}
	once.Do(func() {
		go func() {
			s := newShaper(o, w, h)
			mu.Lock()
			// Something already threw this build away while it ran.
			stale := gen != g
			if !stale {
				state, loaded = s, true
			}
			mu.Unlock()
			if !stale && s != nil && o.Notify != nil {
				o.Notify()
			}
		}()
	})
	return nil, false
}

// CellSize reports the pixel cell the shaper lays out for.
func CellSize() (int, int) {
	mu.RLock()
	defer mu.RUnlock()
	return cellW, cellH
}
