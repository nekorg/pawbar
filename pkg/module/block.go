// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import "go.rockorager.dev/vaxis"

// Block is the style/format surface every module shares and the only part
// of a module's configuration that named states can override. All fields
// are optional; nil means "inherit from the layer below".
//
// Merging is explicit (Over); there is deliberately no reflection here.
type Block struct {
	Fg            *Color    `yaml:"fg"`
	Bg            *Color    `yaml:"bg"`
	Bold          *bool     `yaml:"bold"`
	Dim           *bool     `yaml:"dim"`
	Italic        *bool     `yaml:"italic"`
	Underline     *bool     `yaml:"underline"`
	Blink         *bool     `yaml:"blink"`
	Reverse       *bool     `yaml:"reverse"`
	Strikethrough *bool     `yaml:"strikethrough"`
	Cursor        *Cursor   `yaml:"cursor"`
	Format        *Format   `yaml:"format"`
	Template      *Template `yaml:"template"`
}

// Over returns b layered on top of base: every field b sets wins, every
// field b leaves nil inherits from base. Setting Format masks an inherited
// Template and vice versa, so a state can switch formatting engines.
func (b Block) Over(base Block) Block {
	out := base
	if b.Fg != nil {
		out.Fg = b.Fg
	}
	if b.Bg != nil {
		out.Bg = b.Bg
	}
	if b.Bold != nil {
		out.Bold = b.Bold
	}
	if b.Dim != nil {
		out.Dim = b.Dim
	}
	if b.Italic != nil {
		out.Italic = b.Italic
	}
	if b.Underline != nil {
		out.Underline = b.Underline
	}
	if b.Blink != nil {
		out.Blink = b.Blink
	}
	if b.Reverse != nil {
		out.Reverse = b.Reverse
	}
	if b.Strikethrough != nil {
		out.Strikethrough = b.Strikethrough
	}
	if b.Cursor != nil {
		out.Cursor = b.Cursor
	}
	if b.Format != nil {
		out.Format = b.Format
		out.Template = nil
	}
	if b.Template != nil {
		out.Template = b.Template
		out.Format = nil
	}
	return out
}

// IsZero reports whether no field is set.
func (b Block) IsZero() bool {
	return b == Block{}
}

// Style converts the resolved block to a vaxis style.
func (b Block) Style() vaxis.Style {
	var s vaxis.Style
	if b.Fg != nil {
		s.Foreground = b.Fg.Go()
	}
	if b.Bg != nil {
		s.Background = b.Bg.Go()
	}
	on := func(p *bool) bool { return p != nil && *p }
	if on(b.Bold) {
		s.Attribute |= vaxis.AttrBold
	}
	if on(b.Dim) {
		s.Attribute |= vaxis.AttrDim
	}
	if on(b.Italic) {
		s.Attribute |= vaxis.AttrItalic
	}
	if on(b.Blink) {
		s.Attribute |= vaxis.AttrBlink
	}
	if on(b.Reverse) {
		s.Attribute |= vaxis.AttrReverse
	}
	if on(b.Strikethrough) {
		s.Attribute |= vaxis.AttrStrikethrough
	}
	if on(b.Underline) {
		s.UnderlineStyle = vaxis.UnderlineSingle
	}
	return s
}

// Shape returns the resolved mouse shape (default when unset).
func (b Block) Shape() vaxis.MouseShape {
	if b.Cursor == nil {
		return vaxis.MouseShapeDefault
	}
	return b.Cursor.Go()
}

// Formatter returns the active formatting engine, Template winning over
// Format when both survive a merge, or nil when neither is set.
func (b Block) Formatter() Formatter {
	if b.Template != nil {
		return b.Template
	}
	if b.Format != nil {
		return b.Format
	}
	return nil
}
