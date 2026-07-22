// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package bluetooth

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed bluetooth.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "bluetooth",
		Doc:  "bluetooth adapter/device status via bluez",
		New:  func() module.Module { return &bluetoothModule{} },
		States: []module.StateDef{
			{Name: "disconnected", Doc: "adapter powered, no device connected"},
			{Name: "off", Doc: "adapter is powered off"},
		},
		Placeholders: []module.Placeholder{
			{Name: "device", Doc: "connected device name", Kind: module.KindString},
		},
		Defaults: defaults,
	})
}
