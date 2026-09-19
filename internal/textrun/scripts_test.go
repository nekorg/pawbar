// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package textrun

import (
	"image/color"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Part
	}{
		{"ascii", "cpu 12%", nil},
		{"latin1", "café", nil},
		{"cjk stays terminal text", "日本語 한국어", nil},
		{"nerd font glyph", " arch", nil},
		{"emoji", "🔋 88%", nil},
		{"devanagari alone", "हिन्दी", []Part{{"हिन्दी", true}}},
		{"latin around", "ab हिन्दी cd", []Part{
			{"ab ", false}, {"हिन्दी", true}, {" cd", false},
		}},
		{"punctuation joins two stretches", "राजस्थान - विकिपीडिया", []Part{
			{"राजस्थान - विकिपीडिया", true},
		}},
		{"trailing punctuation is not joined", "हिन्दी - Firefox", []Part{
			{"हिन्दी", true}, {" - Firefox", false},
		}},
		{"digits do not join", "हिन्दी 12 हिन्दी", []Part{
			{"हिन्दी", true}, {" 12 ", false}, {"हिन्दी", true},
		}},
		{"thai", "ไทย", []Part{{"ไทย", true}}},
		{"arabic", "العربية", []Part{{"العربية", true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Split(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestComplex(t *testing.T) {
	for _, s := range []string{"", "plain", "日本語", "🔋", ""} {
		if Complex(s) {
			t.Errorf("Complex(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"हिन्दी", "ab ไทย", " עברית"} {
		if !Complex(s) {
			t.Errorf("Complex(%q) = false, want true", s)
		}
	}
}

// Nothing may be shaped before the shaper is up, and asking must not block
// the caller: the bar draws plain cells for that frame instead.
func TestShapeBeforeReady(t *testing.T) {
	sh := New(Options{})
	if r := sh.Shape("हिन्दी", false, false); r != nil {
		t.Fatalf("Shape returned %v with no cell size set", r)
	}
	// And a bar that never built one at all just draws plain cells.
	if r := (*Shaper)(nil).Shape("हिन्दी", false, false); r != nil {
		t.Fatalf("a nil Shaper shaped %v", r)
	}
	if n := (*Run)(nil).Cells(); n != 0 {
		t.Errorf("(*Run)(nil).Cells() = %d, want 0", n)
	}
	if img, key := (*Run)(nil).Image(4, false, false, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}); img != nil || key != "" {
		t.Errorf("(*Run)(nil).Image() = %v, %q", img, key)
	}
}

// Two surfaces at different DPI each keep their own cell. As one global this
// was a fight: every SetCellSize threw the other surface's shaper away and
// rebuilt it against the wrong cell.
func TestShapersDoNotShareACellSize(t *testing.T) {
	a, b := New(Options{}), New(Options{})
	a.SetCellSize(10, 20)
	b.SetCellSize(14, 28)

	if w, h := a.CellSize(); w != 10 || h != 20 {
		t.Errorf("a.CellSize() = %dx%d, want 10x20", w, h)
	}
	if w, h := b.CellSize(); w != 14 || h != 28 {
		t.Errorf("b.CellSize() = %dx%d, want 14x28", w, h)
	}

	// Moving one must leave the other alone.
	a.SetCellSize(12, 24)
	if w, h := b.CellSize(); w != 14 || h != 28 {
		t.Errorf("b.CellSize() = %dx%d after a resized, want 14x28", w, h)
	}
}
