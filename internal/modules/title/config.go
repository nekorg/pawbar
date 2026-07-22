// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package title

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed title.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "title",
		Doc:  "focused window title (hyprland, i3/sway)",
		New:  func() module.Module { return &titleModule{} },
		States: []module.StateDef{
			// Per-segment state: styles the class chip before the title.
			{Name: "class", Doc: "the window-class chip segment"},
		},
		Placeholders: []module.Placeholder{
			{Name: "title", Doc: "focused window title", Kind: module.KindString},
			{Name: "class", Doc: "focused window class", Kind: module.KindString},
		},
		Defaults: defaults,
	})
}
