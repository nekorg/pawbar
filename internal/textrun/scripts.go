// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package textrun

import "unicode"

// hostile are the scripts a terminal cell grid cannot express. They reorder,
// stack and join, and most of their marks carry real horizontal advance that
// every width table scores as zero, so the terminal allots a fraction of the
// room the text needs and overlaps the rest. These we rasterise ourselves.
//
// Everything absent from this list stays real terminal text: Latin, Greek,
// Cyrillic, CJK, Hangul, emoji, box drawing and the Nerd Font private use
// area all sit in their cells honestly.
var hostile = []*unicode.RangeTable{
	unicode.Devanagari,
	unicode.Bengali,
	unicode.Gurmukhi,
	unicode.Gujarati,
	unicode.Oriya,
	unicode.Tamil,
	unicode.Telugu,
	unicode.Kannada,
	unicode.Malayalam,
	unicode.Sinhala,
	unicode.Thai,
	unicode.Lao,
	unicode.Khmer,
	unicode.Myanmar,
	unicode.Tibetan,
	unicode.Arabic,
	unicode.Hebrew,
	unicode.Syriac,
	unicode.Thaana,
}

// lowestHostile is the first code point any of the above reaches (Hebrew).
const lowestHostile = 0x0591

// Part is a stretch of a segment's text, either ordinary terminal text or one
// shaping unit to rasterise.
type Part struct {
	Text    string
	Complex bool
}

// Complex reports whether s holds anything a cell grid cannot place.
func Complex(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x80 {
			continue
		}
		// past the ascii fast path, look properly
		for _, r := range s[i:] {
			if isHostile(r) {
				return true
			}
		}
		return false
	}
	return false
}

func isHostile(r rune) bool {
	return r >= lowestHostile && unicode.IsOneOf(hostile, r)
}

// joins reports whether r may sit inside a complex run without ending it.
// Spaces and punctuation between two hostile stretches belong to the phrase:
// a shirorekha does not cross a space, but keeping the whole phrase as one
// unit means one image and one set of consistent metrics.
func joins(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// Split cuts s into alternating plain and complex parts, and returns nil when
// none of it needs rasterising, which is the overwhelmingly common case.
func Split(s string) []Part {
	if !Complex(s) {
		return nil
	}

	rs := []rune(s)
	// mark the hostile runes, then let a neutral stretch join the run only
	// when hostile text resumes on the far side of it.
	mark := make([]bool, len(rs))
	for i, r := range rs {
		mark[i] = isHostile(r)
	}
	for i := 0; i < len(rs); i++ {
		if !mark[i] {
			continue
		}
		j := i + 1
		for j < len(rs) && !mark[j] && joins(rs[j]) {
			j++
		}
		if j < len(rs) && mark[j] {
			for k := i + 1; k < j; k++ {
				mark[k] = true
			}
		}
		i = j - 1
	}

	var out []Part
	for i := 0; i < len(rs); {
		j := i
		for j < len(rs) && mark[j] == mark[i] {
			j++
		}
		out = append(out, Part{Text: string(rs[i:j]), Complex: mark[i]})
		i = j
	}
	return out
}
