// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"image"
	"testing"

	"go.rockorager.dev/vaxis"
)

// gradient is a content kind with an uncomparable field, which is exactly
// what chrome will look like. Comparing two of these as interface values
// would panic; Content.Equal is what keeps that out of the render path.
type gradient struct {
	stops []string
}

func (gradient) isContent() {}

func (g gradient) Equal(other Content) bool {
	o, ok := other.(gradient)
	if !ok || len(g.stops) != len(o.stops) {
		return false
	}
	for i := range g.stops {
		if g.stops[i] != o.stops[i] {
			return false
		}
	}
	return true
}

func TestSegmentEqualHandlesUncomparableContent(t *testing.T) {
	a := Segment{Content: gradient{stops: []string{"a", "b"}}}
	b := Segment{Content: gradient{stops: []string{"a", "b"}}}
	c := Segment{Content: gradient{stops: []string{"a", "z"}}}

	// The bare == that slices.Equal would use panics on these.
	if !a.Equal(b) {
		t.Error("equal gradients compared unequal")
	}
	if a.Equal(c) {
		t.Error("different gradients compared equal")
	}
}

func TestImageEqualityIsByKeyNotPointer(t *testing.T) {
	// Two distinct allocations with the same content key: the snapshot
	// de-dupe must see them as the same icon, so a module that re-decodes
	// an unchanged icon does not force a repaint every frame.
	one := image.NewRGBA(image.Rect(0, 0, 8, 8))
	two := image.NewRGBA(image.Rect(0, 0, 8, 8))

	a := Segment{Content: Image{Img: one, Key: "icon:abc", Cells: 2}}
	b := Segment{Content: Image{Img: two, Key: "icon:abc", Cells: 2}}
	if !a.Equal(b) {
		t.Error("same key, different allocation: compared unequal")
	}

	c := Segment{Content: Image{Img: one, Key: "icon:xyz", Cells: 2}}
	if a.Equal(c) {
		t.Error("different keys compared equal")
	}

	d := Segment{Content: Image{Img: one, Key: "icon:abc", Cells: 3}}
	if a.Equal(d) {
		t.Error("different spans compared equal")
	}
}

func TestSegmentEqualComparesStyleAndRouting(t *testing.T) {
	base := Segment{
		Style:   vaxis.Style{Foreground: vaxis.IndexColor(1)},
		Region:  "r",
		Shape:   vaxis.MouseShapeClickable,
		Content: Text{S: "x"},
	}

	same := base
	if !base.Equal(same) {
		t.Fatal("identical segments compared unequal")
	}

	for name, mut := range map[string]func(Segment) Segment{
		"style":   func(s Segment) Segment { s.Style = vaxis.Style{Foreground: vaxis.IndexColor(2)}; return s },
		"region":  func(s Segment) Segment { s.Region = "other"; return s },
		"shape":   func(s Segment) Segment { s.Shape = vaxis.MouseShapeHelp; return s },
		"content": func(s Segment) Segment { s.Content = Text{S: "y"}; return s },
		"shrink":  func(s Segment) Segment { s.Content = Text{S: "x", Shrink: 1}; return s },
	} {
		if base.Equal(mut(base)) {
			t.Errorf("a changed %s compared equal", name)
		}
	}
}

func TestSegmentEqualHandlesNilContent(t *testing.T) {
	var empty Segment
	if !empty.Equal(Segment{}) {
		t.Error("two empty segments compared unequal")
	}
	if empty.Equal(Txt("x")) {
		t.Error("an empty segment compared equal to text")
	}
	if Txt("x").Equal(empty) {
		t.Error("text compared equal to an empty segment")
	}
}

func TestDifferentContentKindsAreNeverEqual(t *testing.T) {
	txt := Segment{Content: Text{S: "x"}}
	img := Segment{Content: Image{Img: image.NewRGBA(image.Rect(0, 0, 1, 1)), Key: "x", Cells: 1}}
	if txt.Equal(img) || img.Equal(txt) {
		t.Error("text and image compared equal")
	}
}
