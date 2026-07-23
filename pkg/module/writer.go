// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"

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
	segs    []Segment
	err     error
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
	s, err := r.Formatter.Render(data)
	if err != nil {
		w.fail(err, o, r)
		return
	}
	w.emit(s, o, r)
}

// Raw emits literal text as one segment, still styled by the cascade.
func (w *Writer) Raw(s string, opts ...SegOpt) {
	o := applyOpts(opts)
	w.emit(s, o, w.resolve(o.states))
}

// Segments returns everything written. Runtime use.
func (w *Writer) Segments() []Segment { return w.segs }

// Err returns the first formatting error hit during this render, if any.
func (w *Writer) Err() error { return w.err }

func applyOpts(opts []SegOpt) segOpts {
	var o segOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func (w *Writer) emit(text string, o segOpts, r Resolved) {
	if text == "" {
		return
	}
	shape := r.Shape
	if o.cursor != nil {
		shape = o.cursor.Go()
	}
	w.segs = append(w.segs, Segment{
		Text:   text,
		Style:  r.Style,
		Region: o.region,
		Shape:  shape,
	})
}

func (w *Writer) fail(err error, o segOpts, r Resolved) {
	if w.err == nil {
		w.err = err
	}
	w.emit("‹fmt›", o, r)
}
