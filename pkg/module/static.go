// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import "strconv"

// Static builds a Def for a module that renders fixed text and never
// updates: separators, spacers. The text ships as the module's default
// `format`, so users can restyle or replace it like any other module.
func Static(name, text string) Def {
	return Def{
		Name:     name,
		Doc:      "static text",
		New:      func() Module { return &staticModule{} },
		Defaults: []byte("format: " + strconv.Quote(text) + "\n"),
	}
}

// Spacer builds a Static Def flagged as a visual gap: its edge cells donate
// their module-facing half to an adjacent module, widening its click hitbox.
func Spacer(name, text string) Def {
	def := Static(name, text)
	def.Spacer = true
	return def
}

type staticModule struct{}

func (m *staticModule) Init(ctx *Ctx) error { return nil }
func (m *staticModule) Render(w *Writer)    { w.Text(nil) }
