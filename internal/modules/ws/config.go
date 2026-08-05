// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ws

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed ws.yaml
var defaults []byte

type Options struct {
	// CurrentOnly renders only what is on screen: the focused workspace,
	// plus the displayed one on any other monitor being shown. Toggle it
	// at runtime with a user state that overrides it, e.g.:
	//
	//	states:
	//	  focus: { current_only: true }
	//	on:
	//	  right: { cycle: [focus] }
	CurrentOnly bool `yaml:"current_only"`
	// Monitor selects whose workspaces to show: "self" (the monitor this
	// bar is on), "all", or an output name.
	Monitor string `yaml:"monitor"`
}

func init() {
	module.Register(module.Def{
		Name:    "ws",
		Doc:     "workspaces (hyprland, i3/sway); click a workspace to switch",
		New:     func() module.Module { return &wsModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "urgent", Doc: "workspace has an urgent window (per segment)"},
			{Name: "active", Doc: "the focused workspace (per segment)"},
			{Name: "visible", Doc: "on screen on its monitor, but not focused (per segment)"},
			{Name: "special", Doc: "a special/scratchpad workspace (per segment)"},
		},
		Placeholders: []module.Placeholder{
			{Name: "ws", Doc: "workspace name", Kind: module.KindString},
		},
		Verbs: []module.VerbDef{
			{Name: "goto", Doc: "switch to the workspace under the pointer"},
		},
		Defaults: defaults,
	})
}
