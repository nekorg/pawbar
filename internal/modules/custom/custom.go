// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package custom

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed custom.yaml
var defaults []byte

// Example config:
//
//	custom:
//	    format: "hello"
//	    fg: "@accent"
//	    states:
//	      alt: { format: "world" }
//	    on:
//	      left: { cycle: [alt] }
//	      right: { run: "pavucontrol" }
func init() {
	module.Register(module.Def{
		Name:     "custom",
		Doc:      "user-defined static text with states and actions",
		New:      func() module.Module { return &customModule{} },
		Defaults: defaults,
	})
}

type customModule struct{}

func (m *customModule) Init(ctx *module.Ctx) error { return nil }
func (m *customModule) Render(w *module.Writer)    { w.Text(module.P{}) }
