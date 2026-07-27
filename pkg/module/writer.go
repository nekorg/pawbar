// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"
	"image"

	"go.rockorager.dev/vaxis"
)

// Segment is one styled run of text a module rendered. Segments are the
// module's complete output snapshot; the runtime lays them out, truncates
// them and maps mouse hits back through Region.
type Segment struct {
	Text   string
	Style  vaxis.Style
	Region string
	Shape  vaxis.MouseShape

	// Shrink > 0 makes this segment elastic: the layout shortens it (in
	// proportion to the weight, fairly against the other elastic segments
	// on the bar) before it truncates anything rigid. See format.Part.
	Shrink int

	// Image, when non-nil, makes this an icon segment: the runtime draws it
	// as a Kitty graphic spanning Cells columns instead of drawing Text.
	// ImageKey identifies the image for caching across frames — it must be
	// fully determined by the image's content, and callers must reuse the
	// same image value for an unchanged key so equal snapshots compare
	// equal (the runtime skips re-publishing unchanged output).
	Image    image.Image
	ImageKey string
	Cells    int
}

// Resolved is the outcome of resolving the style cascade for a set of
// active states: what a segment is drawn with.
type Resolved struct {
	Style     vaxis.Style
	Shape     vaxis.MouseShape
	Formatter Formatter
}

// StyleResolver resolves the module's style cascade with extra per-segment
// states stacked on top. Provided by the runtime.
type StyleResolver func(extraStates []string) Resolved

// Writer collects a module's render output. One Writer is handed to each
// Render call; it is not safe for use outside that call.
type Writer struct {
	resolve StyleResolver
	ops     []op
	err     error
}

// op is one recorded emission, held at every detail level its formatter
// offers. Raw and Icon have a single level and repeat unchanged; a Text op
// with a format ladder has one entry per rung. A level past the end reuses
// the last, so modules with different ladder depths compose.
type op struct {
	levels [][]Segment
}

// NewWriter builds a Writer around a style resolver. Runtime use.
func NewWriter(resolve StyleResolver) *Writer {
	return &Writer{resolve: resolve}
}

type segOpts struct {
	states []string
	region string
	cursor *Cursor
}

// SegOpt adjusts one emitted segment.
type SegOpt func(*segOpts)

// States stacks extra states onto this segment's style resolution. This is how a
// workspaces module styles the active workspace differently from the rest.
func States(names ...string) SegOpt {
	return func(o *segOpts) { o.states = append(o.states, names...) }
}

// Region tags the segment for mouse routing: clicks on it carry the id in
// Mouse.Region / VerbArgs.Region.
func Region(id string) SegOpt {
	return func(o *segOpts) { o.region = id }
}

// WithCursor overrides the pointer shape over this segment.
func WithCursor(c Cursor) SegOpt {
	return func(o *segOpts) { o.cursor = &c }
}

// Text renders the module's active format (or template) with data and
// emits the result as one segment.
func (w *Writer) Text(data P, opts ...SegOpt) {
	o := applyOpts(opts)
	r := w.resolve(o.states)
	if r.Formatter == nil {
		w.fail(fmt.Errorf("no format configured"), o, r)
		return
	}
	levels := make([][]Segment, 0, r.Formatter.Levels())
	for l := range r.Formatter.Levels() {
		parts, err := r.Formatter.RenderParts(l, data)
		if err != nil {
			w.fail(err, o, r)
			return
		}
		// One segment per part, so the layout can shrink the elastic ones
		// without disturbing the rigid text around them.
		var segs []Segment
		for _, p := range parts {
			if seg, ok := w.segment(p.Text, p.Shrink, o, r); ok {
				segs = append(segs, seg)
			}
		}
		levels = append(levels, segs)
	}
	w.ops = append(w.ops, op{levels: levels})
}

// Raw emits literal text as one rigid segment, still styled by the cascade.
func (w *Writer) Raw(s string, opts ...SegOpt) {
	o := applyOpts(opts)
	w.emit(s, 0, o, w.resolve(o.states))
}

// Icon emits an image segment: the runtime draws img as a Kitty graphic
// spanning cells columns, still routing clicks through Region. key
// identifies the image for cross-frame caching and must be fully
// determined by the image's content (re-decoding is the caller's concern;
// it should hand back the same image value for an unchanged key). No-ops
// when img is nil or cells <= 0.
func (w *Writer) Icon(img image.Image, key string, cells int, opts ...SegOpt) {
	if img == nil || cells <= 0 {
		return
	}
	o := applyOpts(opts)
	r := w.resolve(o.states)
	shape := r.Shape
	if o.cursor != nil {
		shape = o.cursor.Go()
	}
	w.fixed(Segment{
		Style:    r.Style,
		Region:   o.region,
		Shape:    shape,
		Image:    img,
		ImageKey: key,
		Cells:    cells,
	})
}

// Levels returns the module's output at each detail level, widest first.
// There is always at least one. Runtime use.
func (w *Writer) Levels() [][]Segment {
	n := 1
	for _, o := range w.ops {
		n = max(n, len(o.levels))
	}
	out := make([][]Segment, n)
	for l := range n {
		for _, o := range w.ops {
			// An op with a shallower ladder holds at its last level.
			out[l] = append(out[l], o.levels[min(l, len(o.levels)-1)]...)
		}
	}
	return out
}

// Segments returns the widest level. Runtime use.
func (w *Writer) Segments() []Segment { return w.Levels()[0] }

// Err returns the first formatting error hit during this render, if any.
func (w *Writer) Err() error { return w.err }

func applyOpts(opts []SegOpt) segOpts {
	var o segOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// segment builds one text segment; empty text emits nothing.
func (w *Writer) segment(text string, shrink int, o segOpts, r Resolved) (Segment, bool) {
	if text == "" {
		return Segment{}, false
	}
	shape := r.Shape
	if o.cursor != nil {
		shape = o.cursor.Go()
	}
	return Segment{
		Text:   text,
		Style:  r.Style,
		Region: o.region,
		Shape:  shape,
		Shrink: shrink,
	}, true
}

// fixed records a segment that is the same at every detail level.
func (w *Writer) fixed(seg Segment) {
	w.ops = append(w.ops, op{levels: [][]Segment{{seg}}})
}

func (w *Writer) emit(text string, shrink int, o segOpts, r Resolved) {
	if seg, ok := w.segment(text, shrink, o, r); ok {
		w.fixed(seg)
	}
}

func (w *Writer) fail(err error, o segOpts, r Resolved) {
	if w.err == nil {
		w.err = err
	}
	w.emit("‹fmt›", 0, o, r)
}
