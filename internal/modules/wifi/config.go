// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package wifi

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed wifi.yaml
var defaults []byte

type Options struct {
	Tick        module.Duration `yaml:"tick"`
	DeviceIndex int             `yaml:"device_index"`
	Icons       []string        `yaml:"icons"`
}

func init() {
	module.Register(module.Def{
		Name:    "wifi",
		Doc:     "wifi status via NetworkManager",
		New:     func() module.Module { return &wifiModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "disconnected", Doc: "no active access point"},
		},
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "signal strength icon", Kind: module.KindString},
			{Name: "ssid", Doc: "connected network name", Kind: module.KindString},
			{Name: "interface", Doc: "wireless interface name", Kind: module.KindString},
			{Name: "strength", Doc: "signal strength percentage", Kind: module.KindNumber},
		},
		Defaults: defaults,
	})
}
