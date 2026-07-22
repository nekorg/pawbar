// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package battery

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed battery.yaml
var defaults []byte

type Options struct {
	DischargingIcons []string       `yaml:"discharging_icons"`
	ChargingIcons    []string       `yaml:"charging_icons"`
	ChargedIcon      string         `yaml:"charged_icon"`
	WarnAt           module.Percent `yaml:"warn_at"`
	LowAt            module.Percent `yaml:"low_at"`
}

func init() {
	module.Register(module.Def{
		Name:    "battery",
		Doc:     "battery level via upower",
		New:     func() module.Module { return &batteryModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "warn", Doc: "battery at or below warn_at"},
			{Name: "low", Doc: "battery at or below low_at"},
			{Name: "charging", Doc: "plugged in and charging"},
			{Name: "charged", Doc: "fully charged"},
		},
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "battery level icon", Kind: module.KindString},
			{Name: "bat", Doc: "battery percentage", Kind: module.KindNumber},
			{Name: "hours", Doc: "hours until full/empty", Kind: module.KindNumber},
			{Name: "minutes", Doc: "minutes until full/empty (0-59)", Kind: module.KindNumber},
		},
		Defaults: defaults,
	})
}
