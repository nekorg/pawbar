// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package textrun

import (
	"image"
	"image/color"
	"strconv"

	"golang.org/x/image/math/fixed"
)

// cacheCap is how many shaped runs or rasterised slices are kept before the
// unused half is dropped. A bar draws a handful of distinct strings; anything
// past this is a title that changes every second, and those are exactly the
// entries worth losing.
const cacheCap = 256

// cached is one drawn slice of a run: the coverage it rasterised to, and that
// coverage in each colour it has been asked for. Colour is not part of the
// raster, so a theme change or a hover style costs a memcpy.
type cached struct {
	mask *image.Alpha
	imgs map[color.NRGBA]*image.NRGBA
	used bool
}

// Image rasterises the part of r that fits in cells columns, at fg.
//
// dropStart and dropEnd say which ends the layout cut, and the ellipsis goes
// inside the image rather than in a cell of its own: the image is a whole
// number of cells wide, so a terminal ellipsis after it would sit up to a
// cell away from the ink it belongs to.
//
// The returned image is exactly cells*cellW by cellH and must not be
// modified; every frame drawing the same thing shares it. The key identifies
// it for the placement cache upstream.
func (r *Run) Image(cells int, dropStart, dropEnd bool, fg color.NRGBA) (*image.NRGBA, string) {
	if r == nil || cells < 1 {
		return nil, ""
	}
	lo, hi := r.fit(cells, dropStart, dropEnd)
	return r.image(lo, hi, cells, dropStart, dropEnd, fg)
}

// Ellipsis is the run reduced to nothing but its ellipsis, for when a trim
// left a column of it standing with no text behind it.
func (r *Run) Ellipsis(cells int, fg color.NRGBA) (*image.NRGBA, string) {
	if r == nil || cells < 1 {
		return nil, ""
	}
	return r.image(0, 0, cells, false, true, fg)
}

func (r *Run) image(lo, hi, cells int, dropStart, dropEnd bool, fg color.NRGBA) (*image.NRGBA, string) {
	s := r.s
	key := r.text + "\x00" + strconv.Itoa(lo) + ":" + strconv.Itoa(hi) +
		":" + strconv.Itoa(cells) + ":" + boolKey(dropStart) + boolKey(dropEnd) +
		":" + strconv.Itoa(int(r.aspect.Weight)) + ":" + strconv.Itoa(int(r.aspect.Style))

	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.images[key]
	if !ok {
		text := ""
		if dropStart {
			text += s.ellipse
		}
		text += string(r.runes[r.cuts[lo]:r.cuts[hi]])
		if dropEnd {
			text += s.ellipse
		}
		c = &cached{
			mask: s.mask(s.shapeLine([]rune(text), r.fams, r.aspect), cells),
			imgs: map[color.NRGBA]*image.NRGBA{},
		}
		sweep(s.images, cacheCap, func(v *cached) *bool { return &v.used })
		s.images[key] = c
	}
	c.used = true
	img, ok := c.imgs[fg]
	if !ok {
		img = colourise(c.mask, fg)
		c.imgs[fg] = img
	}
	return img, key + "\x00" + strconv.Itoa(int(fg.R)) + "," + strconv.Itoa(int(fg.G)) +
		"," + strconv.Itoa(int(fg.B)) + "," + strconv.Itoa(int(fg.A))
}

// fit picks the widest cluster range that still fits in cells columns, once
// room is set aside for whichever ellipses the cut needs. Clusters are the
// only safe granularity: cut anywhere else and a matra is stranded from its
// base or a virama is left hanging.
func (r *Run) fit(cells int, dropStart, dropEnd bool) (int, int) {
	n := len(r.adv)
	if !dropStart && !dropEnd {
		return 0, n
	}

	budget := fixed.I(cells * r.s.cellW)
	if dropStart {
		budget -= r.ellipse
	}
	if dropEnd {
		budget -= r.ellipse
	}
	if budget <= 0 {
		return 0, 0
	}

	lo, hi := 0, n
	switch {
	case dropStart && dropEnd:
		// Give way at both ends alternately, so the middle survives.
		used := r.width
		for used > budget && lo < hi {
			used -= r.adv[lo]
			lo++
			if used > budget && lo < hi {
				hi--
				used -= r.adv[hi]
			}
		}
	case dropStart:
		// The layout kept the trailing columns, so drop leading clusters.
		used := fixed.Int26_6(0)
		lo = n
		for lo > 0 && used+r.adv[lo-1] <= budget {
			lo--
			used += r.adv[lo]
		}
	default:
		used := fixed.Int26_6(0)
		hi = 0
		for hi < n && used+r.adv[hi] <= budget {
			used += r.adv[hi]
			hi++
		}
	}
	return lo, hi
}

// sweep keeps a cache from growing without bound: once it is over cap,
// everything untouched since the last sweep goes, and the rest is put back on
// probation. Cheap, and it never evicts what the bar is currently drawing.
func sweep[K comparable, V any](m map[K]V, cap int, used func(V) *bool) {
	if len(m) < cap {
		return
	}
	for k, v := range m {
		if p := used(v); *p {
			*p = false
			continue
		}
		delete(m, k)
	}
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
