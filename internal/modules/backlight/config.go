// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed backlight.yaml
var defaults []byte

type Options struct {
	Icons []string `yaml:"icons"`

	// Backend selects how brightness is reached: auto, sysfs or ddc. To
	// vary it per monitor, put the entry under a top-level `outputs:`
	// section — that mechanism already narrows the config before a module
	// is built.
	Backend Mode `yaml:"backend"`

	// Monitor is the output to control: "self" for the one this bar is
	// pinned to, or a connector name.
	Monitor string `yaml:"monitor"`

	Step module.Percent `yaml:"step"`

	// Poll is how often a DDC display is re-read. A monitor changed with
	// its own buttons announces nothing, so noticing means asking; 0
	// disables it. Unused by the sysfs backend, which gets udev events.
	Poll module.Duration `yaml:"poll"`
}

func init() {
	module.Register(module.Def{
		Name:    "backlight",
		Doc:     "screen brightness via sysfs/logind or DDC/CI",
		New:     func() module.Module { return &backlightModule{} },
		Options: func() any { return &Options{} },
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "brightness level icon", Kind: module.KindString},
			{Name: "light", Doc: "brightness percentage", Kind: module.KindNumber},
			{Name: "now", Doc: "raw brightness value", Kind: module.KindNumber},
			{Name: "max", Doc: "raw maximum brightness", Kind: module.KindNumber},
			{Name: "backend", Doc: "resolved backend: sysfs or ddc", Kind: module.KindString},
		},
		Verbs: []module.VerbDef{
			{Name: "brightness-up", Doc: "raise brightness by `step`"},
			{Name: "brightness-down", Doc: "lower brightness by `step`"},
			{Name: "set-brightness", Doc: "set brightness to the percentage given as an argument"},
		},
		Defaults: defaults,
	})
}
