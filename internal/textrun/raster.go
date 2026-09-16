// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package textrun

import (
	"image"
	"image/color"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// mask rasterises a line into an 8 bit coverage mask the width of cells
// columns. A mask rather than a bitmap for three reasons: kitty wants
// straight alpha and Go's RGBA is premultiplied, recolouring a mask is a
// memcpy so a theme change costs nothing, and coverage is the only thing
// worth caching since it is what the colour is applied to.
//
// Each glyph gets its own rasterizer pass composited over the mask. Piling
// every outline into one pass would be faster and wrong: Devanagari marks
// overlap their bases, and a counter in one glyph would cancel ink in
// another.
func (s *shaper) mask(line shaping.Line, cells int) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, cells*s.cellW, s.cellH))
	var z vector.Rasterizer
	x := float32(0)
	y := float32(s.met.baseline)
	for _, out := range line {
		scale := float32(s.met.em) / float32(out.Face.Upem())
		for _, g := range out.Glyphs {
			gx := x + fixedToFloat(g.XOffset)
			gy := y - fixedToFloat(g.YOffset)
			if outline, ok := out.Face.GlyphData(g.GlyphID).(font.GlyphOutline); ok {
				drawOutline(&z, m, outline, scale, gx, gy)
			}
			x += fixedToFloat(g.XAdvance)
		}
	}
	return m
}

// drawOutline fills one glyph into the mask. The rasterizer is sized to the
// glyph's own box, so a run costs about as much as its ink and not as much as
// its bounding row.
func drawOutline(z *vector.Rasterizer, dst *image.Alpha, o font.GlyphOutline, scale, x, y float32) {
	if len(o.Segments) == 0 {
		return
	}
	minX, minY := float32(1<<30), float32(1<<30)
	maxX, maxY := float32(-(1 << 30)), float32(-(1 << 30))
	for i := range o.Segments {
		for _, pt := range o.Segments[i].ArgsSlice() {
			px := pt.X*scale + x
			py := -pt.Y*scale + y
			minX, maxX = min(minX, px), max(maxX, px)
			minY, maxY = min(minY, py), max(maxY, py)
		}
	}

	// A pixel of slack each way so antialiased edges are not clipped.
	box := image.Rect(int(minX)-1, int(minY)-1, int(maxX)+2, int(maxY)+2).Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	ox, oy := float32(box.Min.X), float32(box.Min.Y)

	z.Reset(box.Dx(), box.Dy())
	for _, seg := range o.Segments {
		p := func(i int) (float32, float32) {
			return seg.Args[i].X*scale + x - ox, -seg.Args[i].Y*scale + y - oy
		}
		switch seg.Op {
		case opentype.SegmentOpMoveTo:
			z.MoveTo(p(0))
		case opentype.SegmentOpLineTo:
			z.LineTo(p(0))
		case opentype.SegmentOpQuadTo:
			x1, y1 := p(0)
			x2, y2 := p(1)
			z.QuadTo(x1, y1, x2, y2)
		case opentype.SegmentOpCubeTo:
			x1, y1 := p(0)
			x2, y2 := p(1)
			x3, y3 := p(2)
			z.CubeTo(x1, y1, x2, y2, x3, y3)
		}
	}
	z.Draw(dst, box, image.Opaque, image.Point{})
}

// colourise expands a coverage mask into the straight rgba kitty's f=32
// wants, over a transparent ground so the cell's own background, and the
// panel's transparency with it, show through untouched.
//
// No gamma curve: kitty's text_composition_strategy on linux is gamma 1.0 and
// contrast 0, which is plain alpha blending. Inventing a curve here is what
// would make the ink look pasted in.
func colourise(m *image.Alpha, fg color.NRGBA) *image.NRGBA {
	b := m.Bounds()
	out := image.NewNRGBA(b)
	for y := 0; y < b.Dy(); y++ {
		src := m.Pix[y*m.Stride : y*m.Stride+b.Dx()]
		dst := out.Pix[y*out.Stride:]
		for x, a := range src {
			if a == 0 {
				continue
			}
			i := x * 4
			dst[i+0] = fg.R
			dst[i+1] = fg.G
			dst[i+2] = fg.B
			dst[i+3] = uint8(uint32(a) * uint32(fg.A) / 255)
		}
	}
	return out
}

func fixedToFloat(i fixed.Int26_6) float32 { return float32(i) / 64 }
