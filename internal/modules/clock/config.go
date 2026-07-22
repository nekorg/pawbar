// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package clock

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed clock.yaml
var defaults []byte

type Options struct {
	Tick     module.Duration `yaml:"tick"`
	AutoTick bool            `yaml:"auto_tick"`
}

func init() {
	module.Register(module.Def{
		Name:    "clock",
		Doc:     "wall-clock date/time",
		New:     func() module.Module { return &clockModule{} },
		Options: func() any { return &Options{} },
		Placeholders: []module.Placeholder{
			{Name: "time", Doc: "current time (spec is a strftime layout)", Kind: module.KindTime},
		},
		Verbs: []module.VerbDef{
			{Name: "calendar", Doc: "open the calendar menu at the pointer"},
		},
		Defaults: defaults,
	})
}
