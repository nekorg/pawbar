// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed volume.yaml
var defaults []byte

type Options struct {
	Icons []string       `yaml:"icons"`
	Step  module.Percent `yaml:"step"`
}

func init() {
	module.Register(module.Def{
		Name:    "volume",
		Doc:     "default-sink volume via pulseaudio/pipewire",
		New:     func() module.Module { return &volumeModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "muted", Doc: "the default sink is muted"},
		},
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "volume level icon", Kind: module.KindString},
			{Name: "vol", Doc: "volume percentage", Kind: module.KindNumber},
		},
		Verbs: []module.VerbDef{
			{Name: "toggle-mute", Doc: "mute/unmute the default sink"},
			{Name: "volume-up", Doc: "raise volume by `step`"},
			{Name: "volume-down", Doc: "lower volume by `step`"},
		},
		Defaults: defaults,
	})
}
