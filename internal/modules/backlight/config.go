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
}

func init() {
	module.Register(module.Def{
		Name:    "backlight",
		Doc:     "screen brightness via sysfs/udev",
		New:     func() module.Module { return &backlightModule{} },
		Options: func() any { return &Options{} },
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "brightness level icon", Kind: module.KindString},
			{Name: "light", Doc: "brightness percentage", Kind: module.KindNumber},
			{Name: "now", Doc: "raw brightness value", Kind: module.KindNumber},
			{Name: "max", Doc: "raw maximum brightness", Kind: module.KindNumber},
		},
		Defaults: defaults,
	})
}
