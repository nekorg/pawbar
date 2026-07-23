// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sessioncontrols

import (
	_ "embed"

	"github.com/nekorg/pawbar/internal/menus/session"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed sessioncontrols.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "sessioncontrols",
		Doc:  "session menu launcher (lock, logout, shutdown, ...)",
		New:  func() module.Module { return &scModule{} },
		Verbs: []module.VerbDef{
			{Name: "menu", Doc: "open the session menu at the pointer"},
		},
		Defaults: defaults,
	})
}

type scModule struct{}

func (m *scModule) Init(ctx *module.Ctx) error {
	ctx.HandleVerb("menu", func(a module.VerbArgs) error {
		return menus.OpenList(ctx, menus.FromVerb(a), session.Menu())
	})
	return nil
}

func (m *scModule) Render(w *module.Writer) {
	w.Text(module.P{})
}
